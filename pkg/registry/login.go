package registry

import (
	"context"
	"strings"

	"github.com/adevinta/noe/pkg/log"
	"github.com/spf13/afero"
)

type AuthenticationToken struct {
	Kind  string
	Token string
	Ref   AuthenticationSourceRef
}

type AuthenticationSourceRef struct {
	Provider string
}

type Authenticator interface {
	Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken)
}

type AuthenticatorFunc func(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken)

func (f AuthenticatorFunc) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	f(ctx, imagePullSecret, registry, image, tag, candidates)
}

var _ Authenticator = Authenticators{}

type Authenticators []Authenticator

func (a Authenticators) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	for _, auth := range a {
		auth.Authenticate(ctx, imagePullSecret, registry, image, tag, candidates)
	}
}

func NewAuthenticator(kubeletConfigFile, kubeletBinDir string, privateRegistryPaterns []string) Authenticators {

	fs := afero.NewOsFs()
	a := Authenticators{
		ImagePullSecretAuthenticator{DockerConfigAuthenticator: DockerConfigAuthenticator{Provider: "ImagePullSecret"}},
	}
	if kubeletConfigFile != "" && kubeletBinDir != "" {
		a = append(a, KubeletAuthenticator{fs: fs, scheme: newScheme(), BinDir: kubeletBinDir, Config: kubeletConfigFile})
	} else {
		log.DefaultLogger.Info("no kubelet config file or bin dir provided, won't use kubelet authentication")
	}
	a = append(a,
		ContainerDAuthenticator{fs: fs},
		DockerConfigFileAuthenticator{fs: fs, DockerConfigAuthenticator: DockerConfigAuthenticator{Provider: "docker-config"}},
		AnonymousAuthenticator{
			PrivateRegistryPatterns: cleanRegistryPatterns(privateRegistryPaterns),
		},
	)
	return a
}

func cleanRegistryPatterns(registryPatterns []string) []string {
	r := []string{}
	for _, pattern := range registryPatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			r = append(r, pattern)
		}
	}
	return r
}
