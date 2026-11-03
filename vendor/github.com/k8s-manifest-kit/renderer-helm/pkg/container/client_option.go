package container

import (
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/k8s-manifest-kit/pkg/util"
)

// ClientOption is a functional option for the OCI Client.
type ClientOption = util.Option[ClientOptions]

// ClientOptions holds configuration for the OCI registry client.
type ClientOptions struct {
	credential *auth.Credential
	PlainHTTP  bool
}

// ApplyTo applies the client options to the target configuration.
func (opts ClientOptions) ApplyTo(target *ClientOptions) {
	if opts.credential != nil {
		target.credential = opts.credential
	}

	if opts.PlainHTTP {
		target.PlainHTTP = opts.PlainHTTP
	}
}

// WithCredential sets the authentication credentials for the OCI client.
func WithCredential(username string, password string) ClientOption {
	return util.FunctionalOption[ClientOptions](func(opts *ClientOptions) {
		opts.credential = &auth.Credential{
			Username: username,
			Password: password,
		}
	})
}

// WithPlainHTTP enables plain HTTP (non-TLS) communication with the registry.
func WithPlainHTTP(plain bool) ClientOption {
	return util.FunctionalOption[ClientOptions](func(opts *ClientOptions) {
		opts.PlainHTTP = plain
	})
}
