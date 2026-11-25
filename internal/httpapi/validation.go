package httpapi

import (
	"github.com/KareemAraide/distributed-systems-41/api"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http"
	"strings"
)

// Validate against the same document served by /docs, including required fields
// and additionalProperties: false. The validator restores the JSON request body.
func validateAPI(next http.Handler) http.Handler {
	spec, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		panic(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/v1" {
			route, params, err := router.FindRoute(r)
			if err != nil {
				writeProblem(w, 404, "unknown API route")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err = openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route}); err != nil {
				writeProblem(w, 400, "invalid request: "+err.Error())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
