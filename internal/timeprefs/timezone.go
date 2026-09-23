package timeprefs

import (
	"context"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

const Key = "profile_timezone"

type Reader interface {
	GetConfig(context.Context, string) (string, bool, error)
}

func Validate(name string) error {
	if name == "" {
		return nil
	}
	if name == "Local" || len(name) > 100 || strings.ContainsAny(name, "'\"\r\n\\") {
		return fmt.Errorf("use an IANA timezone such as Europe/Paris or America/Martinique")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("unknown timezone; use an IANA name such as Europe/Paris")
	}
	return nil
}
func Read(ctx context.Context, store Reader) (string, *time.Location) {
	if store != nil {
		if name, ok, err := store.GetConfig(ctx, Key); err == nil && ok && name != "" && Validate(name) == nil {
			if loc, err := time.LoadLocation(name); err == nil {
				return name, loc
			}
		}
	}
	return "", time.Local
}

type locationKey struct{}

func WithLocation(ctx context.Context, loc *time.Location) context.Context {
	return context.WithValue(ctx, locationKey{}, loc)
}
func Location(ctx context.Context) *time.Location {
	if loc, ok := ctx.Value(locationKey{}).(*time.Location); ok && loc != nil {
		return loc
	}
	return time.Local
}
func First(loc []*time.Location) *time.Location {
	if len(loc) > 0 && loc[0] != nil {
		return loc[0]
	}
	return time.Local
}
func Parse(layout, value string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(layout, value, loc)
	if err == nil && layout != time.RFC3339 && t.Format(layout) != value {
		return time.Time{}, fmt.Errorf("this local time does not exist in %s", loc)
	}
	return t, err
}
