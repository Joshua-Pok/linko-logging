package linkoerr

import (
	"errors"
	"log/slog"
)

type errWithAttrs struct {
	error
	attrs []slog.Attr
}

func WithAttrs(err error, args ...any) error {
	return &errWithAttrs{
		error: err,
		attrs: argsToAttr(args),
	}
}

func argsToAttr(args []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(args))

	for i := 0; i < len(args); {
		switch key := args[i].(type) {
		case slog.Attr: //if it is a slog attr just append and move on with life
			attrs = append(attrs, key)
			i++
		case string:
			if i+1 >= len(args) {
				attrs = append(attrs, slog.String("!BADKEY", key)) //rmb args is an array with key value pairs so if last value is a key its a bad key
				i++
			} else {
				attrs = append(attrs, slog.Any(key, args[i+1]))
				i += 2 //move past key and value
			}
		default:
			attrs = append(attrs, slog.Any("!BADKEY", args[i]))
			i++
		}
	}

	return attrs
}

func (e *errWithAttrs) unWrap() error {
	return e.error
}

func (e *errWithAttrs) Attrs() []slog.Attr {
	return e.attrs
}

type attrError interface {
	Attrs() []slog.Attr
}

func Attrs(err error) []slog.Attr {
	var attrs []slog.Attr
	for err != nil {
		if ae, ok := err.(attrError); ok {
			attrs = append(attrs, ae.Attrs()...)
		}
		err = errors.Unwrap(err)
	}
	return attrs
}
