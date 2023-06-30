package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml"
	"github.com/spf13/afero"
	"github.com/adevinta/noe/pkg/log"
)

type ContainerdHostConfig struct {
	Capabilities []string         `toml:"capabilities"`
	Header       ContainerdHeader `toml:"header"`
}

type ContainerdHeader struct {
	Authorization string `toml:"authorization"`
}

type ContainerdConfig struct {
	Server string                          `toml:"server"`
	Hosts  map[string]ContainerdHostConfig `toml:"host"`
}

type DockerAuths map[string]DockerAuth

type DockerAuth struct {
	Auth string `json:"auth"`
}

type DockerConfig struct {
	Auths      DockerAuths `json:"auths"`
	CredsStore string      `json:"credsStore"`
}

type AuthenticationToken struct {
	Kind  string
	Token string
}

type ContainerdServerHeader struct {
	Server string
	Header string
}

type Authenticator interface {
	Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string) chan AuthenticationToken
}

var _ Authenticator = RegistryAuthenticator{}

type RegistryAuthenticator struct{}

func (r RegistryAuthenticator) parseDockerConfig(reader io.ReadCloser) (DockerConfig, error) {
	defer reader.Close()
	c := DockerConfig{}
	return c, json.NewDecoder(reader).Decode(&c)
}

func (r RegistryAuthenticator) readDockerConfig() DockerConfig {
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
		fd, err := os.Open(candidate)
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

func (r RegistryAuthenticator) getHeaderOnContainerdFiles(repository string) (ContainerdServerHeader, error) {
	fs := afero.NewOsFs()
	directory := "/etc/containerd" // Replace with the path to the directory you want to walk

	var matchedServerHeader ContainerdServerHeader

	err := afero.Walk(fs, directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		fileExtension := filepath.Ext(path)
		if fileExtension != ".toml" {
			return nil
		}

		configData, err := afero.ReadFile(fs, path)
		if err != nil {
			return nil
		}

		config := ContainerdConfig{}
		err = toml.Unmarshal(configData, &config)
		if err != nil {
			return nil
		}

		if match, _ := regexp.MatchString(repository, config.Server); match {
			log.DefaultLogger.Printf("Get containerd auth for %s", config.Server)
			for _, hostConfig := range config.Hosts {
				header := strings.TrimPrefix(hostConfig.Header.Authorization, "Basic ")
				matchedServerHeader = ContainerdServerHeader{
					Server: config.Server,
					Header: header,
				}
				return nil
			}
		}

		return nil
	})

	if err != nil {
		return ContainerdServerHeader{}, err
	}

	return matchedServerHeader, nil
}

func (r RegistryAuthenticator) getAuthCandidates(ctx context.Context, cfg DockerConfig, registry, image string) chan string {
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
		containerdAuth, _ := r.getHeaderOnContainerdFiles(registry)
		if containerdAuth.Header != "" {
			log.DefaultLogger.WithContext(ctx).WithField("registry", containerdAuth.Server).WithField("image", fmt.Sprintf("%s/%s", registry, image)).Printf("Image matches registry config. Trying it")
			select {
			case candidates <- containerdAuth.Header:
			case <-ctx.Done():
				return
			}
		}

	}()
	return candidates
}

// TODO: change to handle www-authenticate for HTTP/1.1 401 Unauthorized responses
// Www-Authenticate: Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:user/image:pull"
// Www-Authenticate: Bearer realm="https://auth.ipv6.docker.com/token",service="registry.docker.io"
func getDockerHubToken(ctx context.Context, registry, image, tag, token string) string {
	authHost := registry
	if authHost == "docker.io" {
		authHost = "auth.docker.io"
		registry = "registry." + registry
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://%s/token?service=%s&scope=repository:%s:pull", authHost, registry, image), nil)
	if err != nil {
		return token
	}
	req = req.WithContext(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	authResponse := registryAuthResponse{}
	err = json.NewDecoder(resp.Body).Decode(&authResponse)
	if err != nil {
		return ""
	}
	return authResponse.Token
}

func (r RegistryAuthenticator) tryAllCandidates(ctx context.Context, cfg DockerConfig, registry, image, tag string, candidates chan AuthenticationToken) {
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
	select {
	case candidates <- AuthenticationToken{}:
	case <-ctx.Done():
		return
	}
}

func (r RegistryAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string) chan AuthenticationToken {
	cfg := r.readDockerConfig()
	candidates := make(chan AuthenticationToken)
	go func() {
		defer close(candidates)
		if imagePullSecret != "" {
			imagePullSecretConfig := DockerConfig{}
			if err := json.NewDecoder(strings.NewReader(imagePullSecret)).Decode(&imagePullSecretConfig); err != nil {
				// TODO: log
			} else {
				r.tryAllCandidates(ctx, imagePullSecretConfig, registry, image, tag, candidates)
			}
		}
		r.tryAllCandidates(ctx, cfg, registry, image, tag, candidates)
	}()

	return candidates
}
