package validation

import (
	"reflect"
	"strings"
	"sync"

	playground "github.com/go-playground/validator/v10"
)

// Struct-based validation is a thin wrapper around go-playground/validator.
// We translate its FieldError stream into our *Errors bag so the rest of
// your stack only deals with one error shape.
//
// Example:
//
//	type CreateUserRequest struct {
//	    Name  string `json:"name" validate:"required,max=255"`
//	    Email string `json:"email" validate:"required,email"`
//	    Age   int    `json:"age" validate:"required,gte=18,lte=120"`
//	}
//
//	var req CreateUserRequest
//	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { ... }
//	if errs := validation.ValidateStruct(req); errs.Any() { ... }

var (
	pgOnce     sync.Once
	pgInstance *playground.Validate
)

func playgroundValidator() *playground.Validate {
	pgOnce.Do(func() {
		pgInstance = playground.New(playground.WithRequiredStructEnabled())
		// Use json tags for field names so error keys match the wire format.
		// Falls back to the Go field name when no json tag is present.
		pgInstance.RegisterTagNameFunc(func(fld reflect.StructField) string {
			tag := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if tag == "" || tag == "-" {
				return fld.Name
			}
			return tag
		})
	})
	return pgInstance
}

// ValidateStruct runs go-playground/validator against s and returns our
// error bag. s should be a struct or *struct.
//
// We map the playground tag names to our message templates where they
// overlap (required, email, url, uuid). For tags we don't have a custom
// message for, we fall back to "_default" so errors are still readable.
func ValidateStruct(s any) *Errors {
	errs := NewErrors()
	if err := playgroundValidator().Struct(s); err != nil {
		var fes playground.ValidationErrors
		if rerr, ok := err.(playground.ValidationErrors); ok {
			fes = rerr
		}
		for _, fe := range fes {
			field := fe.Field()
			tag := fe.Tag()
			params := map[string]string{}
			if fe.Param() != "" {
				// playground encodes params per-tag — for gte/lte/min/max,
				// the param is a number we surface as :min/:max in the
				// message template.
				switch tag {
				case "min", "gte":
					params["min"] = fe.Param()
				case "max", "lte":
					params["max"] = fe.Param()
				default:
					params["value"] = fe.Param()
				}
			}
			tpl, ok := defaultMessages[mapPlaygroundTag(tag)]
			if !ok {
				tpl = defaultMessages["_default"]
			}
			errs.Add(field, formatMessage(tpl, field, params))
		}
	}
	return errs
}

// mapPlaygroundTag normalizes a few common playground tags to the rule
// names our default message table uses, so users get nicer messages
// without registering anything.
func mapPlaygroundTag(tag string) string {
	switch tag {
	case "gte":
		return "min"
	case "lte":
		return "max"
	case "oneof":
		return "in"
	}
	return tag
}

// RegisterStructTag registers a custom struct-tag validator on the
// underlying playground instance. Use this for project-wide tags like
// `validate:"e164"` or `validate:"objectid"`.
func RegisterStructTag(tag string, fn playground.Func) error {
	return playgroundValidator().RegisterValidation(tag, fn)
}
