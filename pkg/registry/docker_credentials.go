package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adevinta/noe/pkg/log"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
)

type DockerAuths map[string]DockerAuth

type DockerAuth struct {
	Auth string `json:"auth"`
}

type DockerConfig struct {
	Auths      DockerAuths `json:"auths"`
	CredsStore string      `json:"credsStore"`
}

type DockerConfigAuthenticator struct {
	scheme               *runtime.Scheme
	KubeletAuthenticator Authenticator
}

func (r DockerConfigAuthenticator) parseDockerConfig(reader io.ReadCloser) (DockerConfig, error) {
	defer reader.Close()
	c := DockerConfig{}
	return c, json.NewDecoder(reader).Decode(&c)
}

func (r DockerConfigAuthenticator) getAuthCandidates(ctx context.Context, cfg DockerConfig, registry, image string) chan string {
	candidates := make(chan string)
	go func() {
		defer close(candidates)
		if cfg.CredsStore != "" {
			if registry == "docker.io" {
				registry = "index.docker.io"
			}
			cmd := exec.Command("docker-credential-"+cfg.CredsStore, "get")
			cmd.Stdin = strings.NewReader(registry)
			b := bytes.Buffer{}
			cmd.Stdout = &b
			err := cmd.Run()
			if err != nil {
				// TODO: log for higher verbosity levels
			} else {
				login := map[string]string{}
				err := json.NewDecoder(&b).Decode(&login)
				if err != nil {
					// TODO: log for higher verbosity levels
				} else {
					select {
					case candidates <- base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", login["Username"], login["Secret"]))):
					case <-ctx.Done():
						return
					}
				}
			}
		}
		for reg, auth := range cfg.Auths {
			if reg == "https://index.docker.io/v1/" {
				reg = "docker.io"
			}
			// Implement kubernetes lookups: https://kubernetes.io/docs/concepts/containers/images/#config-json
			// TODO: match does not seem to always work
			if matched, err := filepath.Match(reg, fmt.Sprintf("%s/%s", registry, image)); err == nil && (matched || reg == registry) {
				if auth.Auth != "" {
					log.DefaultLogger.WithContext(ctx).WithField("registry", reg).WithField("image", fmt.Sprintf("%s/%s", registry, image)).Printf("Image matches registry config. Trying it")
					select {
					case candidates <- auth.Auth:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return candidates
}

func (r DockerConfigAuthenticator) Authenticate(ctx context.Context, cfg DockerConfig, registry, image, tag string, candidates chan AuthenticationToken) {
	for auth := range r.getAuthCandidates(ctx, cfg, registry, image) {
		select {
		case candidates <- AuthenticationToken{
			Kind:  "Basic",
			Token: auth,
		}:
		case <-ctx.Done():
			return
		}
	}
}

type ImagePullSecretAuthenticator struct {
	DockerConfigAuthenticator
}

var _ Authenticator = ImagePullSecretAuthenticator{}

func (r ImagePullSecretAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	if imagePullSecret != "" {
		cfg := DockerConfig{}
		if err := json.NewDecoder(strings.NewReader(imagePullSecret)).Decode(&cfg); err != nil {
			log.DefaultLogger.WithContext(ctx).WithError(err).Error("failed to decode imagePullSecret")
		} else {
			r.DockerConfigAuthenticator.Authenticate(ctx, cfg, registry, image, tag, candidates)
		}
	}
}

type DockerConfigFileAuthenticator struct {
	fs afero.Fs
	DockerConfigAuthenticator
}

var _ Authenticator = DockerConfigFileAuthenticator{}

func (r DockerConfigFileAuthenticator) readDockerConfig() DockerConfig {
	// Read more: https://kubernetes.io/docs/concepts/containers/images/#config-json
	// https://v1-21.docs.kubernetes.io/docs/concepts/containers/images/#configuring-nodes-to-authenticate-to-a-private-registry
	// and: https://stackoverflow.com/a/65356707
	candidates := []string{
		"/var/lib/kubelet/config.json", // TODO: add the kubelet --prefix option
		// TODO: add {cwd of kubelet}/config.json option
	}
	if envVal, ok := os.LookupEnv("DOCKER_CONFIG"); ok {
		candidates = append(candidates, filepath.Join(envVal, "config.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".docker/config.json"))
	}
	if home, ok := os.LookupEnv("HOME"); ok {
		candidates = append(candidates, filepath.Join(home, ".docker/config.json"))
	}
	candidates = append(candidates,
		"/.docker/config.json",
		"/var/lib/kubelet/.dockercfg",
		// TODO add {--root-dir:-/var/lib/kubelet}/.dockercfg option
	)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".dockercfg"))
	}
	if home, ok := os.LookupEnv("HOME"); ok {
		candidates = append(candidates, filepath.Join(home, ".dockercfg"))
	}
	candidates = append(candidates, "/.dockercfg")
	for _, candidate := range candidates {
		fd, err := r.fs.Open(candidate)
		if err != nil {
			// TODO: log for higher verbosity levels
			continue
		}
		cfg, err := r.parseDockerConfig(fd)
		if err != nil {
			// TODO: log for higher verbosity levels
			continue
		}
		for registry := range cfg.Auths {
			log.DefaultLogger.WithField("registry", registry).WithField("candidate", candidate).Debug("loaded registry auth config")
		}
		return cfg
	}
	return DockerConfig{}
}

func (r DockerConfigFileAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	cfg := r.readDockerConfig()

	r.DockerConfigAuthenticator.Authenticate(ctx, cfg, registry, image, tag, candidates)
}
