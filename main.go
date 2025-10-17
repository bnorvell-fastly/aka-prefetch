// Prefetch
//
// Compatible with Akamai prefetch :
//  https://techdocs.akamai.com/adaptive-media-delivery/docs/origin-assist-prefetch
//
// TO origin from Edge : CDN-Origin-Assist-Prefetch-Enabled:1 to enable prefetch
// FROM origin to edge : CDN-Origin-Assist-Prefetch-Path: <absolute|relative path of prefetch-able object's URL>

package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/fastly/compute-sdk-go/fsthttp"
)

// Implement pre-caching objects based on https://techdocs.akamai.com/adaptive-media-delivery/docs/origin-assist-prefetch

func main() {
	fsthttp.ServeFunc(func(ctx context.Context, w fsthttp.ResponseWriter, r *fsthttp.Request) {
		var DEBUG = false

		// DEBUG logging
		if r.Header.Get("FASTLY-DEBUG") != "" {
			DEBUG = true
		}

		// Log service version
		if DEBUG {
			fmt.Printf("=> Service Version : %s running on %s from %s <=\n",
				os.Getenv("FASTLY_SERVICE_VERSION"), os.Getenv("FASTLY_HOSTNAME"), r.RemoteAddr)
		}

		// Filter requests that have unexpected methods.
		if r.Method != "GET" {
			w.WriteHeader(fsthttp.StatusMethodNotAllowed)
			fmt.Fprintf(w, "This method is not allowed\n")
			return
		}

		// Enable origin pre-fetch
		r.Header.Add("CDN-Origin-Assist-Prefetch-Enabled", "1")

		// Forward the request to backend
		resp, err := r.Send(ctx, "origin")
		if err != nil {
			w.WriteHeader(fsthttp.StatusBadGateway)
			fmt.Fprintln(w, err)
			return
		}

		w.Header().Reset(resp.Header)

		// Remove processing headers from client response
		// Leave them here for debugging if we toggle it
		if !DEBUG {
			w.Header().Del("CDN-Origin-Assist-Prefetch-Enabled")
			w.Header().Del("CDN-Origin-Assist-Prefetch-Path")
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		w.Close() // Send the response back to the client, and continue processing below

		// Did we get a response header with instructions to prefetch ?
		prefetchObjects := resp.Header.Values("CDN-Origin-Assist-Prefetch-Path")
		var prefetchList []string

		// Build the prefetch object list
		if len(prefetchObjects) > 0 {
			if DEBUG {
				fmt.Println("=> Prefetching")
			}

			for _, headerValue := range prefetchObjects {
				// Split by comma - Akamai spec supports comma-separated lists
				paths := strings.Split(headerValue, ",")
				for _, object := range paths {
					object = strings.TrimSpace(object)
					if object == "" {
						continue
					}

					var newPath string
					if strings.HasPrefix(object, "/") {
						// Absolute path
						newPath = fmt.Sprintf("%s://%s%s", r.URL.Scheme, r.URL.Host, object)
					} else {
						// Relative path - resolve from current directory
						dir := path.Dir(r.URL.Path)
						newPath = fmt.Sprintf("%s://%s%s/%s", r.URL.Scheme, r.URL.Host, dir, object)
					}

					// Copy the Query Parameters from the original request.
					newURL, err := url.Parse(newPath)
					if err != nil {
						fmt.Printf("Prefetch path parse error: %s (from : %s)", newPath, headerValue)
						continue
					}
					newURL.RawQuery = r.URL.RawQuery

					prefetchList = append(prefetchList, newURL.String())
				}
			}
		}

		// If we've populated a list, try them in sequence to populate the cache.
		var fetched = 0
		for _, object := range prefetchList {

			if DEBUG {
				fmt.Println("=> fetching : ", object)
			}

			// Compute runtime limitations are 32 backend requests per invocation
			// so lets terminate gracefully before that point.
			if fetched > 24 {
				fmt.Printf("=> Backend limit reached after %d objects, terminating gracefully", fetched)
				break
			}

			newURL, err := url.Parse(object)
			if err == nil {
				// Clone the request so that we can use it to get prefetch element(s)
				// Leave the prefetch header intact, so that the cached respones have the prefetch
				// list if it was returned. This allows future cache respones to allow for prefetch
				// as well. Since we're not processing the header in this loop, it is not in danger
				// of recursively calling for more objects
				var preReq = r.Clone()
				preReq.URL = newURL
				resp, err := preReq.Send(ctx, "origin")

				// Check for errors and non-ok status returns
				// Loop to next object in list if it's not good
				// discard any body for memory safety.
				if err == nil && resp.StatusCode == fsthttp.StatusOK {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}

				// Pass or fail, increment counter. Errors count against runtime limits
				fetched++

				if DEBUG {
					fmt.Printf("==> Got response %d for object %s", resp.StatusCode, preReq.URL.String())
				}
			}
		}
	})
}
