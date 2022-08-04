package registry

import "context"

type RegistryFunc func(ctx context.Context, imagePullSecret, image string) ([]Platform, error)

func (f RegistryFunc) ListArchs(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
	return f(ctx, imagePullSecret, image)
}
