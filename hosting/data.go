package hosting

import (
	"context"
	"prism/internal/server"
)

// PersonalData is an owner-bound database resource, not a Prism application.
// The embedding host must supply a restricted DSN for this owner's namespace.
// Credentials and encryption keys must never be sourced from request fields.
type PersonalData struct{ data *server.PersonalData }

func OpenPersonalData(ctx context.Context, owner, dsn string, key []byte) (*PersonalData, error) {
	d, err := server.OpenPersonalData(ctx, owner, dsn, key)
	if err != nil {
		return nil, err
	}
	return &PersonalData{data: d}, nil
}
func (d *PersonalData) Close(ctx context.Context) error { return d.data.Close(ctx) }
