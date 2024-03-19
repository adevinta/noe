package registry

import "context"

type AnonymousAuthenticator struct{}

func (r AnonymousAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	select {
	case candidates <- AuthenticationToken{}:
	case <-ctx.Done():
		return
	}
}
