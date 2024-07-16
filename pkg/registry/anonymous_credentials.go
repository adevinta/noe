package registry

import (
	"context"

	"github.com/adevinta/noe/pkg/log"
)

type AnonymousAuthenticator struct {
	PrivateRegistryPatterns []string
}

func (r AnonymousAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	for _, pattern := range r.PrivateRegistryPatterns {
		if imageMatchesLoginPattern(ctx, registry, image, pattern) {
			log.DefaultLogger.WithContext(ctx).WithField("pattern", pattern).WithField("image", image).WithField("registry", registry).Error("image is registered as private. Skipping anonymous authentication")
			return
		}
	}
	select {
	case candidates <- AuthenticationToken{}:
	case <-ctx.Done():
		return
	}
}
