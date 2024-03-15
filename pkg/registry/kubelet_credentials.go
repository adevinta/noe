package registry

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adevinta/noe/pkg/log"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/yaml"
	kubeletconfigv1 "k8s.io/kubelet/config/v1"
	kubeletcredentialsprovider "k8s.io/kubelet/pkg/apis/credentialprovider"
	kubeletcredentialsproviderv1 "k8s.io/kubelet/pkg/apis/credentialprovider/v1"
	kubeletcredentialsproviderv1alpha1 "k8s.io/kubelet/pkg/apis/credentialprovider/v1alpha1"
	kubeletcredentialsproviderv1beta1 "k8s.io/kubelet/pkg/apis/credentialprovider/v1beta1"
)

var (
	execCommandOutput = execCommandOutputImpl
)

var _ Authenticator = KubeletAuthenticator{}

type KubeletAuthenticator struct {
	fs     afero.Fs
	scheme *runtime.Scheme
	BinDir string
	Config string
}

func execCommandOutputImpl(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, env []string, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = kubeletcredentialsprovider.AddToScheme(s)
	_ = kubeletcredentialsproviderv1alpha1.AddToScheme(s)
	_ = kubeletcredentialsproviderv1beta1.AddToScheme(s)
	_ = kubeletcredentialsproviderv1.AddToScheme(s)

	_ = kubeletcredentialsproviderv1alpha1.RegisterConversions(s)
	_ = kubeletcredentialsproviderv1beta1.RegisterConversions(s)
	_ = kubeletcredentialsproviderv1.RegisterConversions(s)
	_ = kubeletconfigv1.AddToScheme(s)

	return s
}

func parseObjects(scheme *runtime.Scheme, r io.Reader, object runtime.Object) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	dfltGVK := (&kubeletcredentialsproviderv1.CredentialProviderResponse{}).GroupVersionKind()
	obj, _, err := serializer.NewCodecFactory(scheme).UniversalDeserializer().Decode(data, &dfltGVK, object)
	if err != nil {
		return err
	}
	err = scheme.Convert(obj, object, nil)
	if err != nil {
		return err
	}
	return nil

}

func serialiseObjects(scheme *runtime.Scheme, w io.Writer, object runtime.Object, gv runtime.GroupVersioner) error {
	err := serializer.NewCodecFactory(scheme).EncoderForVersion(
		json.NewSerializerWithOptions(
			json.DefaultMetaFactory,
			scheme,
			scheme,
			json.SerializerOptions{
				Yaml:   false,
				Strict: true,
				Pretty: false,
			}),
		gv,
	).Encode(object, w)
	if err != nil {
		return err
	}
	return nil
}

// imageMatchesLoginPattern implements the detection of a match between an image and a matchImage
// for both CredentialProviderConfig and CredentialProviderResponse
func imageMatchesLoginPattern(ctx context.Context, registry, image, pattern string) bool {

	// CredentialProviderConfig
	// see https://kubernetes.io/docs/reference/config-api/kubelet-config.v1/
	// https://github.com/kubernetes/kubelet/blob/d555812529d5925f8753a522576ea2968e6c75f6/config/v1/types.go#L53C1-L57C65
	// Each entry in matchImages is a pattern which can optionally contain a port and a path.
	// Globs can be used in the domain, but not in the port or the path. Globs are supported
	// as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'.
	// Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match
	// a single subdomain segment, so *.io does not match *.k8s.io.
	//
	// A match exists between an image and a matchImage when all of the below are true:
	// - Both contain the same number of domain parts and each part matches.
	// - The URL path of an imageMatch must be a prefix of the target image URL path.
	// - If the imageMatch contains a port, then the port must match in the image as well.
	//
	// Example values of matchImages:
	//   - 123456789.dkr.ecr.us-east-1.amazonaws.com
	//   - *.azurecr.io
	//   - gcr.io
	//   - *.*.registry.io
	//   - registry.io:8080/path

	// CredentialProviderResponse
	// see https://kubernetes.io/docs/reference/config-api/kubelet-credentialprovider.v1/#credentialprovider-kubelet-k8s-io-v1-CredentialProviderResponse
	// https://github.com/kubernetes/kubelet/blob/d555812529d5925f8753a522576ea2968e6c75f6/pkg/apis/credentialprovider/v1/types.go#L103
	// Each key is a match image string (more on this below). The corresponding authConfig value
	// should be valid for all images that match against this key. A plugin should set
	// this field to null if no valid credentials can be returned for the requested image.
	//
	// Each key in the map is a pattern which can optionally contain a port and a path.
	// Globs can be used in the domain, but not in the port or the path. Globs are supported
	// as subdomains like '*.k8s.io' or 'k8s.*.io', and top-level-domains such as 'k8s.*'.
	// Matching partial subdomains like 'app*.k8s.io' is also supported. Each glob can only match
	// a single subdomain segment, so *.io does not match *.k8s.io.
	//
	// The kubelet will match images against the key when all of the below are true:
	// - Both contain the same number of domain parts and each part matches.
	// - The URL path of an imageMatch must be a prefix of the target image URL path.
	// - If the imageMatch contains a port, then the port must match in the image as well.
	//
	// When multiple keys are returned, the kubelet will traverse all keys in reverse order so that:
	// - longer keys come before shorter keys with the same prefix
	// - non-wildcard keys come before wildcard keys with the same prefix.
	//
	// For any given match, the kubelet will attempt an image pull with the provided credentials,
	// stopping after the first successfully authenticated pull.
	//
	// Example keys:
	//   - 123456789.dkr.ecr.us-east-1.amazonaws.com
	//   - *.azurecr.io
	//   - gcr.io
	//   - *.*.registry.io
	//   - registry.io:8080/path

	regPath := strings.Split(pattern, "/")
	matchReg := regPath[0]
	matchPath := ""
	if len(regPath) > 1 {
		matchPath = regPath[1]
	}
	matchHostPort := strings.Split(matchReg, ":")
	matchHost := matchHostPort[0]
	matchPort := ""
	if len(matchHostPort) > 1 {
		matchPort = matchHostPort[1]
	}
	matched, err := filepath.Match(matchHost, strings.Split(registry, ":")[0])
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Warn("error matching kubelet credentials provider config")
		return false
	}
	if matched {
		regHostPort := strings.Split(registry, ":")
		if matchPort != "" && (len(regHostPort) < 2 || regHostPort[1] != matchPort) {
			log.DefaultLogger.WithContext(ctx).Debug("image registry does not match registry port")
			return false
		}
		if matchPath != "" && strings.HasPrefix(image, matchPath) {
			log.DefaultLogger.WithContext(ctx).Debug("image registry does not match registry path")
			return false
		}
		return true
	}
	log.DefaultLogger.WithContext(ctx).Debug("image registry does not match registry host")
	return false
}

func providerMatchesImage(ctx context.Context, provider kubeletconfigv1.CredentialProvider, registry, image string) bool {
	for _, match := range provider.MatchImages {
		ctx := log.AddLogFieldsToContext(ctx, logrus.Fields{"matchImage": match})
		if imageMatchesLoginPattern(ctx, registry, image, match) {
			return true
		}
		log.DefaultLogger.WithContext(ctx).Info("image does not match kubelet credentials provider config, skipping it")
	}
	return false
}

func kubeToExec(envVars []kubeletconfigv1.ExecEnvVar) []string {
	env := make([]string, len(envVars))
	for i, v := range envVars {
		env[i] = fmt.Sprintf("%s=%s", v.Name, v.Value)
	}
	return env
}

func (r KubeletAuthenticator) tryIndividualKubeletProvider(ctx context.Context, registry, image string, provider kubeletconfigv1.CredentialProvider, candidates chan AuthenticationToken) {
	if r.BinDir == "" {
		log.DefaultLogger.WithContext(ctx).Error("kubelet authentication BinDir is empty, skipping kubelet credentials provider")
		return
	}
	stdin := bytes.Buffer{}
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}
	req := kubeletcredentialsprovider.CredentialProviderRequest{
		Image: fmt.Sprintf("%s/%s", registry, strings.TrimPrefix(image, "/")),
	}
	gv, err := schema.ParseGroupVersion(provider.APIVersion)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not parse kubelet credentials provider APIVersion, skipping it")
		return
	}

	err = serialiseObjects(r.scheme, &stdin, &req, gv)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not serialize kubelet credentials provider request, skipping it")
		return
	}

	err = execCommandOutput(ctx, &stdin, &stdout, &stderr, kubeToExec(provider.Env), filepath.Join(r.BinDir, provider.Name), provider.Args...)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not execute kubelet credentials provider, skipping it")
		return
	}
	if stderr.Len() > 0 {
		log.DefaultLogger.WithContext(ctx).WithField("error", "stderr").Warn(stderr.String())
		return
	}
	response := kubeletcredentialsprovider.CredentialProviderResponse{}
	err = parseObjects(r.scheme, &stdout, &response)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not parse kubelet credentials provider response, skipping it")
		return
	}
	// TODO: implement the kubelet preference for the order of the keys:
	//
	// When multiple keys are returned, the kubelet will traverse all keys in reverse order so that:
	// - longer keys come before shorter keys with the same prefix
	// - non-wildcard keys come before wildcard keys with the same prefix.
	for key, value := range response.Auth {
		ctx := log.AddLogFieldsToContext(ctx, logrus.Fields{"matchingImage": key})
		if imageMatchesLoginPattern(ctx, registry, image, key) {
			log.DefaultLogger.WithContext(ctx).Debug("matched kubelet credentials provider")
			candidates <- AuthenticationToken{
				Kind:  "Basic",
				Token: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", value.Username, value.Password))),
			}
		} else {
			log.DefaultLogger.WithContext(ctx).Info("image does not match kubelet credentials provider response, skipping it")
		}
	}
}

func (r KubeletAuthenticator) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	if r.Config == "" {
		log.DefaultLogger.WithContext(ctx).Error("ImageCredentialsProviderConfig.Config is empty, skipping kubelet credentials provider")
		return
	}
	if r.BinDir == "" {
		log.DefaultLogger.WithContext(ctx).Error("ImageCredentialsProviderConfig.BinDir is empty, skipping kubelet credentials provider")
		return
	}
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"CredentialProviderConfig": r.Config, "CredentialProviderBinDir": r.BinDir})
	data, err := afero.ReadFile(r.fs, r.Config)

	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not open kubelet credentials provider config, skipping it")
		return
	}
	config := kubeletconfigv1.CredentialProviderConfig{}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Could not decode kubelet credentials provider config, skipping it")
		return
	}
	for _, provider := range config.Providers {
		ctx := log.AddLogFieldsToContext(ctx, logrus.Fields{"provider": provider.Name})
		if providerMatchesImage(ctx, provider, registry, image) {
			log.DefaultLogger.WithContext(ctx).Info("matched kubelet credentials provider")
			r.tryIndividualKubeletProvider(ctx, registry, image, provider, candidates)
		} else {
			log.DefaultLogger.WithContext(ctx).Info("image does not match kubelet credentials provider config, skipping it")
		}
	}
}
