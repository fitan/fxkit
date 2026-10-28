package huma

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fitan/fxkit/crudx"
)

// ResourceHandlers is the optional REST surface [RegisterResource] needs.
// List should be implemented with [crudx.List]; writes stay as ordinary service methods.
type ResourceHandlers[ListRow, Detail, CreateReq, UpdateReq any] interface {
	List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[ListRow], error)
	Get(ctx context.Context, id string) (Detail, error)
	Create(ctx context.Context, req CreateReq) (Detail, error)
	Update(ctx context.Context, req UpdateReq) (Detail, error)
	Delete(ctx context.Context, id string) error
}

// RegisterResourceInput configures path prefixes and operation metadata.
type RegisterResourceInput[UpdateReq any] struct {
	Path        string   // e.g. "/users"
	Tags        []string // OpenAPI tags
	ListSummary string
	// BindUpdate extracts the path id into UpdateReq before calling Update.
	// If nil, PUT is not registered.
	BindUpdate func(id string, body UpdateReq) UpdateReq

	// Optional operation toggles (default false = enabled)
	DisableList   bool // If true, GET {path} is not registered
	DisableGet    bool // If true, GET {path}/{id} is not registered
	DisableCreate bool // If true, POST {path} is not registered
	DisableUpdate bool // If true, PUT {path}/{id} is not registered even if BindUpdate is set
	DisableDelete bool // If true, DELETE {path}/{id} is not registered

	// PascalCaseOperationID generates PascalCase operationId (e.g. ListArticles / GetArticles).
	// Default false keeps backwards compatibility with existing specs (listarticles).
	PascalCaseOperationID bool
}

// ResourceOperations returns the Huma operations [RegisterResource] mounts.
// Use with [authz.HTTPRoutesFromOperations] when also publishing HTTPRoute;
// with auto_register_routes the OpenAPI scrape already covers these paths.
func ResourceOperations[UpdateReq any](in RegisterResourceInput[UpdateReq]) []huma.Operation {
	path := in.Path
	tags := in.Tags
	listSummary := in.ListSummary
	if listSummary == "" {
		listSummary = "List"
	}
	opSuffix := sanitizeOpID(path, in.PascalCaseOperationID)

	var ops []huma.Operation
	if !in.DisableList {
		prefix := "list"
		if in.PascalCaseOperationID {
			prefix = "List"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     listSummary,
			Tags:        tags,
		})
	}
	if !in.DisableGet {
		prefix := "get"
		if in.PascalCaseOperationID {
			prefix = "Get"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path + "/{id}",
			Summary:     "Get",
			Tags:        tags,
		})
	}
	if !in.DisableCreate {
		prefix := "create"
		if in.PascalCaseOperationID {
			prefix = "Create"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPost,
			Path:        path,
			Summary:     "Create",
			Tags:        tags,
		})
	}
	if in.BindUpdate != nil && !in.DisableUpdate {
		prefix := "update"
		if in.PascalCaseOperationID {
			prefix = "Update"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPut,
			Path:        path + "/{id}",
			Summary:     "Update",
			Tags:        tags,
		})
	}
	if !in.DisableDelete {
		prefix := "delete"
		if in.PascalCaseOperationID {
			prefix = "Delete"
		}
		ops = append(ops, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodDelete,
			Path:        path + "/{id}",
			Summary:     "Delete",
			Tags:        tags,
		})
	}
	return ops
}

type idPath struct {
	ID string `path:"id"`
}

type listOut[T any] struct {
	Body crudx.ListResult[T]
}

type detailOut[T any] struct {
	Body T
}

type createIn[T any] struct {
	Body T
}

type updateIn[T any] struct {
	ID   string `path:"id"`
	Body T
}

type emptyOut struct{}

// RegisterResource optionally mounts the five JSON routes for a resource.
// Use it when you want a uniform REST shape; a single [Register] is enough for list-only APIs.
//
//	GET    {path}       list (ZStack q=)
//	GET    {path}/{id}  get
//	POST   {path}       create
//	PUT    {path}/{id}  update (requires BindUpdate)
//	DELETE {path}/{id}  delete
//
// Intended for JSON/Huma resources on the shared chi mux.
func RegisterResource[ListRow, Detail, CreateReq, UpdateReq any](
	api huma.API,
	svc ResourceHandlers[ListRow, Detail, CreateReq, UpdateReq],
	in RegisterResourceInput[UpdateReq],
) {
	path := in.Path
	tags := in.Tags
	listSummary := in.ListSummary
	if listSummary == "" {
		listSummary = "List"
	}
	opSuffix := sanitizeOpID(path, in.PascalCaseOperationID)

	if !in.DisableList {
		prefix := "list"
		if in.PascalCaseOperationID {
			prefix = "List"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     listSummary,
			Tags:        tags,
		}, func(ctx context.Context, input *struct {
			ListQueryInput
		}) (*listOut[ListRow], error) {
			params, err := ListParamsFromInput(&input.ListQueryInput)
			if err != nil {
				return nil, err
			}
			out, err := svc.List(ctx, params)
			if err != nil {
				return nil, err
			}
			return &listOut[ListRow]{Body: out}, nil
		})
	}

	if !in.DisableGet {
		prefix := "get"
		if in.PascalCaseOperationID {
			prefix = "Get"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodGet,
			Path:        path + "/{id}",
			Summary:     "Get",
			Tags:        tags,
		}, func(ctx context.Context, input *idPath) (*detailOut[Detail], error) {
			out, err := svc.Get(ctx, input.ID)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if !in.DisableCreate {
		prefix := "create"
		if in.PascalCaseOperationID {
			prefix = "Create"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPost,
			Path:        path,
			Summary:     "Create",
			Tags:        tags,
		}, func(ctx context.Context, input *createIn[CreateReq]) (*detailOut[Detail], error) {
			out, err := svc.Create(ctx, input.Body)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if in.BindUpdate != nil && !in.DisableUpdate {
		prefix := "update"
		if in.PascalCaseOperationID {
			prefix = "Update"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodPut,
			Path:        path + "/{id}",
			Summary:     "Update",
			Tags:        tags,
		}, func(ctx context.Context, input *updateIn[UpdateReq]) (*detailOut[Detail], error) {
			req := in.BindUpdate(input.ID, input.Body)
			out, err := svc.Update(ctx, req)
			if err != nil {
				return nil, err
			}
			return &detailOut[Detail]{Body: out}, nil
		})
	}

	if !in.DisableDelete {
		prefix := "delete"
		if in.PascalCaseOperationID {
			prefix = "Delete"
		}
		Register(api, huma.Operation{
			OperationID: prefix + opSuffix,
			Method:      http.MethodDelete,
			Path:        path + "/{id}",
			Summary:     "Delete",
			Tags:        tags,
		}, func(ctx context.Context, input *idPath) (*emptyOut, error) {
			if err := svc.Delete(ctx, input.ID); err != nil {
				return nil, err
			}
			return &emptyOut{}, nil
		})
	}
}

func sanitizeOpID(path string, pascalCase bool) string {
	if !pascalCase {
		var b strings.Builder
		for _, r := range path {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// PascalCase (e.g. "/order-items" -> "OrderItems")
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '-' || r == '_'
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}
