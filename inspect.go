package main

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/docker/docker/api"
	"github.com/docker/docker/reference"
	"github.com/docker/docker/registry"
	engineTypes "github.com/docker/engine-api/types"
	containerTypes "github.com/docker/engine-api/types/container"
	"golang.org/x/net/context"
)

// fallbackError wraps an error that can possibly allow fallback to a different
// endpoint.
type fallbackError struct {
	// err is the error being wrapped.
	err error
	// confirmedV2 is set to true if it was confirmed that the registry
	// supports the v2 protocol. This is used to limit fallbacks to the v1
	// protocol.
	confirmedV2 bool
}

// Error renders the FallbackError as a string.
func (f fallbackError) Error() string {
	return f.err.Error()
}

type manifestFetcher interface {
	Fetch(ctx context.Context, ref reference.Named) (*imageInspect, error)
}

type imageInspect struct {
	// I shouldn't need json tag here...
	ID              string `json:"Id"`
	RepoTags        []string
	RepoDigests     []string
	Parent          string
	Comment         string
	Created         string
	Container       string
	ContainerConfig *containerTypes.Config
	DockerVersion   string
	Author          string
	Config          *containerTypes.Config
	Architecture    string
	Os              string
	Size            int64
	Registry        string
}

func inspect(c *cli.Context) (*imageInspect, error) {
	ref, err := reference.ParseNamed(c.Args().First())
	if err != nil {
		return nil, err
	}

	var (
		ii *imageInspect
	)

	if ref.Hostname() != "" {
		ii, err = getData(ref)
		if err != nil {
			return nil, err
		}
		return ii, nil
	}

	authConfig, err := getAuthConfig(c, ref)
	if err != nil {
		return nil, err
	}

	_ = authConfig
	// TODO(runcom): ...
	// both authConfig and unqualified images

	return nil, nil
}

func getData(ref reference.Named) (*imageInspect, error) {
	repoInfo, err := registry.ParseRepositoryInfo(ref)
	if err != nil {
		return nil, err
	}
	if err := validateRepoName(repoInfo.Name()); err != nil {
		return nil, err
	}

	registryService := registry.NewService(nil)

	// FATA[0000] open /etc/docker/certs.d/myreg.com:4000: permission denied
	// need to be run as root, really? :(
	// just pass tlsconfig via cli?!?!?!
	//
	// this happens only with private registry, docker.io works out of the box
	//
	// TODO(runcom): do not assume docker is installed on the system!
	// just fallback as for getAuthConfig
	endpoints, err := registryService.LookupPullEndpoints(repoInfo)
	if err != nil {
		return nil, err
	}

	var (
		ctx                    = context.Background()
		lastErr                error
		discardNoSupportErrors bool
		imgInspect             *imageInspect
		confirmedV2            bool
	)

	for _, endpoint := range endpoints {
		if confirmedV2 && endpoint.Version == registry.APIVersion1 {
			logrus.Debugf("Skipping v1 endpoint %s because v2 registry was detected", endpoint.URL)
			continue
		}
		logrus.Debugf("Trying to fetch image manifest of %s repository from %s %s", repoInfo.Name(), endpoint.URL, endpoint.Version)

		//fetcher, err := newManifestFetcher(endpoint, repoInfo, config)
		fetcher, err := newManifestFetcher(endpoint, repoInfo)
		if err != nil {
			lastErr = err
			continue
		}

		if imgInspect, err = fetcher.Fetch(ctx, ref); err != nil {
			// Was this fetch cancelled? If so, don't try to fall back.
			fallback := false
			select {
			case <-ctx.Done():
			default:
				if fallbackErr, ok := err.(fallbackError); ok {
					fallback = true
					confirmedV2 = confirmedV2 || fallbackErr.confirmedV2
					err = fallbackErr.err
				}
			}
			if fallback {
				if _, ok := err.(registry.ErrNoSupport); !ok {
					// Because we found an error that's not ErrNoSupport, discard all subsequent ErrNoSupport errors.
					discardNoSupportErrors = true
					// save the current error
					lastErr = err
				} else if !discardNoSupportErrors {
					// Save the ErrNoSupport error, because it's either the first error or all encountered errors
					// were also ErrNoSupport errors.
					lastErr = err
				}
				continue
			}
			logrus.Debugf("Not continuing with error: %v", err)
			return nil, err
		}

		return imgInspect, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoints found for %s", ref.String())
	}

	return nil, lastErr
}

func newManifestFetcher(endpoint registry.APIEndpoint, repoInfo *registry.RepositoryInfo) (manifestFetcher, error) {
	switch endpoint.Version {
	case registry.APIVersion2:
		return &v2ManifestFetcher{
			endpoint: endpoint,
			//config:   config,
			repoInfo: repoInfo,
		}, nil
		//case registry.APIVersion1:
		//return &v1ManifestFetcher{
		//endpoint: endpoint,
		////config:   config,
		//repoInfo: repoInfo,
		//}, nil
	}
	return nil, fmt.Errorf("unknown version %d for registry %s", endpoint.Version, endpoint.URL)
}

func getAuthConfig(c *cli.Context, ref reference.Named) (engineTypes.AuthConfig, error) {

	// use docker/cliconfig
	// if no /.docker -> docker not installed fallback to require username|password
	// maybe prompt user:passwd?

	//var (
	//authConfig engineTypes.AuthConfig
	//username   = c.GlobalString("username")
	//password   = c.GlobalString("password")
	//)
	//if username != "" && password != "" {
	//authConfig = engineTypes.AuthConfig{
	//Username: username,
	//Password: password,
	//}
	//}

	return engineTypes.AuthConfig{}, nil
}

func validateRepoName(name string) error {
	if name == "" {
		return fmt.Errorf("Repository name can't be empty")
	}
	if name == api.NoBaseImageSpecifier {
		return fmt.Errorf("'%s' is a reserved name", api.NoBaseImageSpecifier)
	}
	return nil
}