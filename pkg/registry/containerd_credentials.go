package registry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adevinta/noe/pkg/log"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
)

var _ Authenticator = ContainerDAuthenticator{}

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

type ContainerdServerHeader struct {
	Server string
	Header string
}

type ContainerDAuthenticator struct {
	fs afero.Fs
}

func (r ContainerDAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"authenticator": "ContainerD"})
	containerdAuth, _ := r.getHeaderOnContainerdFiles(registry, "/etc/containerd")
	if containerdAuth.Header != "" {
		log.DefaultLogger.WithContext(ctx).WithField("registry", containerdAuth.Server).WithField("image", fmt.Sprintf("%s/%s", registry, image)).Printf("Image matches registry config. Trying it")
		select {
		case candidates <- AuthenticationToken{
			Kind:  "Basic",
			Token: containerdAuth.Header,
			Ref: AuthenticationSourceRef{
				Provider: "containerD",
			},
		}:
		case <-ctx.Done():
			return
		}
	}
}

func (r ContainerDAuthenticator) getHeaderOnContainerdFiles(repository, directory string) (ContainerdServerHeader, error) {
	var matchedServerHeader ContainerdServerHeader

	err := afero.Walk(r.fs, directory, func(path string, info os.FileInfo, err error) error {
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

		configData, err := afero.ReadFile(r.fs, path)
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
