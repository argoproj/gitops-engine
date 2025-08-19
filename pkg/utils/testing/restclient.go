package testing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/dynamic"
	restfake "k8s.io/client-go/rest/fake"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
)

func NewFakeRESTClientBackedByDynamic(gv schema.GroupVersion, dc dynamic.Interface) *restfake.RESTClient {
	rt := func(req *http.Request) (*http.Response, error) {
		gvr, ns, name, subres, err := parse(req.URL.Path, gv)
		if err != nil {
			return errResp(req, http.StatusBadRequest, err), nil
		}
		// We’ll ignore subresources here; easy to extend if you need "status", etc.
		var ri dynamic.ResourceInterface
		ri = dc.Resource(gvr)
		if ns != "" {
			ri = dc.Resource(gvr).Namespace(ns)
		}

		ctx := context.Background()
		switch req.Method {
		case http.MethodGet:
			if name == "" {
				list, err := ri.List(ctx, metav1.ListOptions{})
				return objResp(req, list, statusFromErr(err)), nil
			}
			obj, err := ri.Get(ctx, name, metav1.GetOptions{})
			return objResp(req, obj, statusFromErr(err)), nil

		case http.MethodPost:
			u, rerr := readUnstructured(req.Body)
			if rerr != nil {
				return errResp(req, http.StatusBadRequest, rerr), nil
			}
			created, err := ri.Create(ctx, u, metav1.CreateOptions{})
			if err != nil {
				return errResp(req, statusFromErr(err), err), nil
			}
			return objResp(req, created, http.StatusCreated), nil

		case http.MethodPut:
			u, rerr := readUnstructured(req.Body)
			if rerr != nil {
				return errResp(req, http.StatusBadRequest, rerr), nil
			}
			updated, err := ri.Update(ctx, u, metav1.UpdateOptions{})
			return objResp(req, updated, statusFromErr(err)), nil

		case http.MethodPatch:
			patch, _ := io.ReadAll(req.Body)
			pt := parsePatchType(req.Header.Get("Content-Type"))
			patched, err := ri.Patch(ctx, name, pt, patch, metav1.PatchOptions{}, subresOpt(subres)...)
			return objResp(req, patched, statusFromErr(err)), nil

		case http.MethodDelete:
			err := ri.Delete(ctx, name, metav1.DeleteOptions{})
			if err != nil {
				return errResp(req, statusFromErr(err), err), nil
			}
			// Respond like apiserver: 200 + Status or empty body is fine for tests
			return objResp(req, map[string]string{"status": "Success"}, http.StatusOK), nil

		default:
			return errResp(req, http.StatusNotImplemented, fmt.Errorf("verb %q not implemented", req.Method)), nil
		}
	}

	return &restfake.RESTClient{
		GroupVersion:         gv,
		Client:               restfake.CreateHTTPClient(rt),
		NegotiatedSerializer: resource.UnstructuredPlusDefaultContentConfig().NegotiatedSerializer,
	}
}

func parse(path string, defaultGroupVersion schema.GroupVersion) (schema.GroupVersionResource, string, string, string, error) {
	seg := strings.Split(strings.Trim(path, "/"), "/")
	if len(seg) < 2 {
		return schema.GroupVersionResource{}, "", "", "", fmt.Errorf("bad path: %s", path)
	}
	gv := defaultGroupVersion
	i := 0
	switch seg[0] {
	case "api":
		// Handles /api/v1/...
		if len(seg) < 2 {
			return schema.GroupVersionResource{}, "", "", "", errors.New("missing version")
		}
		gv = schema.GroupVersion{Version: seg[1]}
		i = 2
	case "apis":
		// Handles /apis/<group>/<version>/...,
		if len(seg) < 3 {
			return schema.GroupVersionResource{}, "", "", "", errors.New("missing group/version")
		}
		gv = schema.GroupVersion{Group: seg[1], Version: seg[2]}
		i = 3
	}

	ns := ""
	if i+1 < len(seg) && seg[i] == "namespaces" {
		ns = seg[i+1]
		i += 2
	}
	if i >= len(seg) {
		return schema.GroupVersionResource{}, "", "", "", errors.New("missing resource")
	}
	res := seg[i]
	i++
	name := ""
	if i < len(seg) {
		name = seg[i]
		i++
	}
	subres := ""
	if i < len(seg) {
		subres = seg[i]
	}
	return gv.WithResource(res), ns, name, subres, nil
}

func readUnstructured(r io.Reader) (*unstructured.Unstructured, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	u := &unstructured.Unstructured{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &u.Object); err != nil {
			return nil, fmt.Errorf("failed to unmarshal request body: %w", err)
		}
	}
	return u, nil
}

func objResp(req *http.Request, obj any, code int) *http.Response {
	var b []byte
	if obj != nil {
		b, _ = json.Marshal(obj)
	}

	return &http.Response{
		StatusCode: code,
		Header:     cmdtesting.DefaultHeader(),
		Body:       cmdtesting.BytesBody(b),
		Request:    req,
	}
}

func errResp(req *http.Request, code int, err error) *http.Response {
	msg := map[string]any{
		"kind":   "Status",
		"code":   code,
		"status": "Failure",
		"reason": err.Error(),
	}
	return objResp(req, msg, code)
}

// statusFromErr returns the HTTP status code you'd expect the apiserver to emit
// for the given Kubernetes-style error. nil => 200 OK.
func statusFromErr(err error) int {
	if err == nil {
		return http.StatusOK
	}

	switch {
	case apierrors.IsNotFound(err):
		return http.StatusNotFound // 404

	case apierrors.IsAlreadyExists(err), apierrors.IsConflict(err):
		return http.StatusConflict // 409

	case apierrors.IsInvalid(err):
		return http.StatusUnprocessableEntity // 422

	case apierrors.IsBadRequest(err):
		return http.StatusBadRequest // 400

	case apierrors.IsUnauthorized(err):
		return http.StatusUnauthorized // 401

	case apierrors.IsForbidden(err):
		return http.StatusForbidden // 403

	case apierrors.IsMethodNotSupported(err):
		return http.StatusMethodNotAllowed // 405

	case apierrors.IsNotAcceptable(err):
		return http.StatusNotAcceptable // 406

	case apierrors.IsUnsupportedMediaType(err):
		return http.StatusUnsupportedMediaType // 415

	case apierrors.IsRequestEntityTooLargeError(err):
		return http.StatusRequestEntityTooLarge // 413

	case apierrors.IsTooManyRequests(err):
		return http.StatusTooManyRequests // 429

	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout // 504

	case apierrors.IsResourceExpired(err), apierrors.IsGone(err):
		return http.StatusGone // 410

	case apierrors.IsServiceUnavailable(err):
		return http.StatusServiceUnavailable // 503

	case apierrors.IsUnexpectedServerError(err):
		return http.StatusInternalServerError // 500
	}

	// If the error implements APIStatus and carries a non-zero code, honor it.
	if s, ok := err.(apierrors.APIStatus); ok {
		if code := s.Status().Code; code != 0 {
			return int(code)
		}
	}

	// Final fallback
	return http.StatusInternalServerError // 500
}

func parsePatchType(ct string) types.PatchType {
	// Very small helper; adjust if you need strategic/json/merge detection
	if strings.Contains(ct, "application/merge-patch+json") {
		return types.PatchType("application/merge-patch+json")
	}
	if strings.Contains(ct, "application/apply-patch+yaml") {
		return types.PatchType("application/apply-patch+yaml")
	}
	return types.PatchType("application/json-patch+json")
}

func subresOpt(sub string) []string {
	if sub == "" {
		return nil
	}
	return []string{sub}
}
