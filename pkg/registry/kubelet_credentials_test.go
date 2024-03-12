package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kubeletconfigv1 "k8s.io/kubelet/config/v1"
)

func TestImageMatchesLoginPattern(t *testing.T) {
	ctx := context.TODO()
	assert.True(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", "*.dkr.*.*.amazonaws.com"))
	assert.True(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", "*.dkr.*.*.amazonaws.com/path"))
	assert.True(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com:5000", "/path/to/image", "*.dkr.*.*.amazonaws.com/path"))

	assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", "*.dkr.eks.*.amazonaws.com"), "when the host does not match, it must not match")
	assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", "*.dkr.*.*.amazonaws.com:5000/path"), "when specified, the port number must match")
	assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "/path", "*.dkr.*.*.amazonaws.com:5000/path/to"), "when specified, the port number must match")
	t.Run("When a wildcard is provided outside from the domain", func(t *testing.T) {
		assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com:5000", "path/to/image", "*.dkr.*.*.amazonaws.com:*/path"), "post must not match globs")
		assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com", "path/to/image", "*.dkr.*.*.amazonaws.com/path/*"), "path must not match globs")
	})
	assert.Falsef(t, imageMatchesLoginPattern(ctx, "account.dkr.ecr.region.amazonaws.com:5000", "path/to/image", `*.dkr.*.*[.amazonaws.com`), "when the pattern is invalid, the image must not match")
}

func TestProviderMatchesImage(t *testing.T) {
	ctx := context.TODO()
	assert.True(t, providerMatchesImage(
		ctx,
		kubeletconfigv1.CredentialProvider{
			Name: "ecr-credential-provider",
			MatchImages: []string{
				"*.dkr.*.*.amazonaws.com",
				"*.dkr.*.region.amazonaws.com",
			},
		},
		"account.dkr.ecr.region.amazonaws.com",
		"/path/to/image",
	))
	assert.True(t, providerMatchesImage(
		ctx,
		kubeletconfigv1.CredentialProvider{
			Name: "ecr-credential-provider",
			MatchImages: []string{
				"*.dkr.eks.*.amazonaws.com",
				"*.dkr.*.*.amazonaws.com",
			},
		},
		"account.dkr.ecr.region.amazonaws.com",
		"/path/to/image",
	))
	assert.False(t, providerMatchesImage(
		ctx,
		kubeletconfigv1.CredentialProvider{
			Name: "ecr-credential-provider",
			MatchImages: []string{
				"*.dkr.eks.*.amazonaws.com",
				"*.dkr.*.*.ghcr.com",
			},
		},
		"account.dkr.ecr.region.amazonaws.com",
		"/path/to/image",
	))
}

func testIndividualKubeletProviderForVersion(t *testing.T, apiVersion string) {
	t.Helper()
	t.Run(fmt.Sprintf("with version %s", apiVersion), func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
			BinDir: "/usr/bin",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)

		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {
			assert.Equal(t, "/usr/bin/ecr-credential-provider", command)
			assert.Empty(t, args)
			input := map[string]interface{}{}
			require.NoError(t, json.NewDecoder(stdin).Decode(&input))
			assert.Equal(
				t,
				map[string]interface{}{
					"kind":       "CredentialProviderRequest",
					"apiVersion": apiVersion,
					"image":      "account.dkr.ecr.region.amazonaws.com/path/to/image",
				},
				input,
			)
			require.NoError(t, json.NewEncoder(stdout).Encode(map[string]interface{}{
				"kind":       "CredentialProviderResponse",
				"apiVersion": apiVersion,
				"auth": map[string]interface{}{
					"*.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
					"account.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
					"*.dkr.ecr.region.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
					"*.ghcr.io": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
				},
			}))

			return nil
		}
		publishedCandidates := 0
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: apiVersion,
			}, candidates)
			close(candidates)
		}()
		for candidate := range candidates {
			publishedCandidates++
			assert.Equal(t, "dXNlcm5hbWU6cGFzc3dvcmQ=", candidate.Token)
			assert.Equal(t, "Basic", candidate.Kind)
		}
		assert.EqualValues(t, 3, publishedCandidates)
	})
}

func TestTryIndividualKubeletProvider(t *testing.T) {
	t.Cleanup(func() {
		execCommandOutput = execCommandOutputImpl
	})
	testIndividualKubeletProviderForVersion(t, "credentialprovider.kubelet.k8s.io/v1alpha1")
	testIndividualKubeletProviderForVersion(t, "credentialprovider.kubelet.k8s.io/v1beta1")
	testIndividualKubeletProviderForVersion(t, "credentialprovider.kubelet.k8s.io/v1")
	t.Run("When the kubelet authenticator is not configured", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v1",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})
	t.Run("When the kubelet authenticator is not configured", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v1",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the configuration group version is invalid", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
			BinDir: "/usr/bin",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v1/Something",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the configuration group version does not exist", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
			BinDir: "/usr/bin",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v0",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the exec command fails", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
			BinDir: "/usr/bin",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {
			return fmt.Errorf("error")
		}
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v1",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the exec command returns an invalid object", func(t *testing.T) {
		authenticator := KubeletAuthenticator{
			scheme: newScheme(),
			BinDir: "/usr/bin",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {
			stdout.Write([]byte("[]"))
			return nil
		}
		go func() {
			authenticator.tryIndividualKubeletProvider(ctx, "account.dkr.ecr.region.amazonaws.com", "/path/to/image", kubeletconfigv1.CredentialProvider{
				Name: "ecr-credential-provider",
				MatchImages: []string{
					"*.dkr.ecr.*.amazonaws.com",
					"*.dkr.ecr.*.amazonaws.com.cn",
					"*.dkr.ecr-fips.*.amazonaws.com",
					"*.dkr.ecr.*.c2s.ic.gov",
					"*.dkr.ecr.*.sc2s.sgov.gov",
				},
				APIVersion: "credentialprovider.kubelet.k8s.io/v1",
			}, candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})
}

func TestAuthenticate(t *testing.T) {
	t.Run("When the image is configured in kubelet", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		cfg := map[string]interface{}{
			"apiVersion": "kubelet.config.k8s.io/v1alpha1",
			"kind":       "CredentialProviderConfig",
			"providers": []map[string]interface{}{
				{
					"name": "ecr-credential-provider",
					"matchImages": []string{
						"*.dkr.ecr.*.amazonaws.com",
						"*.dkr.ecr.*.amazonaws.com.cn",
						"*.dkr.ecr-fips.*.amazonaws.com",
						"*.dkr.ecr.*.c2s.ic.gov",
						"*.dkr.ecr.*.sc2s.sgov.gov",
					},
					"defaultCacheDuration": "12h",
					"apiVersion":           "credentialprovider.kubelet.k8s.io/v1",
				},
			},
		}
		fd, err := fs.Create("/var/lib/kubelet/credentials-provider.yaml")
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(fd).Encode(cfg))

		authenticator := KubeletAuthenticator{
			fs:     fs,
			scheme: newScheme(),
			BinDir: "/usr/bin",
			Config: "/var/lib/kubelet/credentials-provider.yaml",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {

			require.NoError(t, json.NewEncoder(stdout).Encode(map[string]interface{}{
				"kind":       "CredentialProviderResponse",
				"apiVersion": "credentialprovider.kubelet.k8s.io/v1",
				"auth": map[string]interface{}{
					"*.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
				},
			}))
			return nil
		}
		go func() {
			authenticator.Authenticate(ctx, "", "account.dkr.ecr.region.amazonaws.com", "/path/to/image", "", candidates)
			close(candidates)
		}()
		publishedCandidates := 0
		for candidate := range candidates {
			publishedCandidates++
			assert.Equal(t, "dXNlcm5hbWU6cGFzc3dvcmQ=", candidate.Token)
			assert.Equal(t, "Basic", candidate.Kind)
		}
		assert.EqualValues(t, 1, publishedCandidates)
	})

	t.Run("When no image matches in the kubelet configuration", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		cfg := map[string]interface{}{
			"apiVersion": "kubelet.config.k8s.io/v1alpha1",
			"kind":       "CredentialProviderConfig",
			"providers": []map[string]interface{}{
				{
					"name": "ecr-credential-provider",
					"matchImages": []string{
						"*.dkr.ecr.*.amazonaws.com",
						"*.dkr.ecr.*.amazonaws.com.cn",
						"*.dkr.ecr-fips.*.amazonaws.com",
						"*.dkr.ecr.*.c2s.ic.gov",
						"*.dkr.ecr.*.sc2s.sgov.gov",
					},
					"defaultCacheDuration": "12h",
					"apiVersion":           "credentialprovider.kubelet.k8s.io/v1",
				},
			},
		}
		fd, err := fs.Create("/var/lib/kubelet/credentials-provider.yaml")
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(fd).Encode(cfg))

		authenticator := KubeletAuthenticator{
			fs:     fs,
			scheme: newScheme(),
			BinDir: "/usr/bin",
			Config: "/var/lib/kubelet/credentials-provider.yaml",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {

			require.NoError(t, json.NewEncoder(stdout).Encode(map[string]interface{}{
				"kind":       "CredentialProviderResponse",
				"apiVersion": "credentialprovider.kubelet.k8s.io/v1",
				"auth": map[string]interface{}{
					"*.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
				},
			}))
			return nil
		}
		go func() {
			authenticator.Authenticate(ctx, "", "ghrc.io", "/path/to/image", "", candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the kubelet config path does not exist", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		authenticator := KubeletAuthenticator{
			fs:     fs,
			scheme: newScheme(),
			BinDir: "/usr/bin",
			Config: "/var/lib/kubelet/credentials-provider.yaml",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {

			require.NoError(t, json.NewEncoder(stdout).Encode(map[string]interface{}{
				"kind":       "CredentialProviderResponse",
				"apiVersion": "credentialprovider.kubelet.k8s.io/v1",
				"auth": map[string]interface{}{
					"*.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
				},
			}))
			return nil
		}
		go func() {
			authenticator.Authenticate(ctx, "", "ghrc.io", "/path/to/image", "", candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})

	t.Run("When the kubelet contains an invalid yaml", func(t *testing.T) {
		fs := afero.NewMemMapFs()

		afero.WriteFile(fs, "/var/lib/kubelet/credentials-provider.yaml", []byte(`This is not a yaml`), 0644)

		authenticator := KubeletAuthenticator{
			fs:     fs,
			scheme: newScheme(),
			BinDir: "/usr/bin",
			Config: "/var/lib/kubelet/credentials-provider.yaml",
		}
		ctx := context.TODO()
		candidates := make(chan AuthenticationToken)
		execCommandOutput = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, command string, args ...string) error {

			require.NoError(t, json.NewEncoder(stdout).Encode(map[string]interface{}{
				"kind":       "CredentialProviderResponse",
				"apiVersion": "credentialprovider.kubelet.k8s.io/v1",
				"auth": map[string]interface{}{
					"*.dkr.ecr.*.amazonaws.com": map[string]interface{}{
						"username": "username",
						"password": "password",
					},
				},
			}))
			return nil
		}
		go func() {
			authenticator.Authenticate(ctx, "", "ghrc.io", "/path/to/image", "", candidates)
			close(candidates)
		}()
		for range candidates {
			t.Errorf("no candidate must be published")
		}
	})
}
