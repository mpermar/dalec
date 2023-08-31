package rpmbundle

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/frontend/gateway/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	targetBuildroot = "buildroot"
	targetResolve   = "resolve"
	targetSpec      = "spec"
	targetRPM       = "rpm"
	targetSources   = "sources"
)

type reexecFrontend interface {
	CurrentFrontend() (*llb.State, error)
}

func loadSpec(ctx context.Context, client *dockerui.Client) (*frontend.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := frontend.LoadSpec(bytes.TrimSpace(src.Data), client.BuildArgs)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	return spec, nil
}

func handleSubrequest(ctx context.Context, bc *dockerui.Client) (*client.Result, bool, error) {
	return bc.HandleSubrequest(ctx, dockerui.RequestHandler{
		ListTargets: func(ctx context.Context) (*targets.List, error) {
			_, err := loadSpec(ctx, bc)
			if err != nil {
				return nil, err
			}

			return &targets.List{
				Targets: []targets.Target{
					{
						Name:        targetBuildroot,
						Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
					},
					{
						Name:        targetResolve,
						Description: "Outputs the resolved yaml spec with build args expanded. This is primarly intended for debugging purposes.",
					},
					{
						Name:        targetSpec,
						Description: "Like " + targetBuildroot + " but outputs just SPECS/. This is useful for putting the generated spec into a VCS repository.",
					},
					{
						Name:        targetSources,
						Description: "Like " + targetBuildroot + " but outputs just SOURCES/. Thise is useful to pre-hydrate the sources directory.",
					},
					{
						Name:        targetRPM,
						Description: "Builds the rpm and outputs to RPMS/<rpmarch>.",
						Default:     true,
					},
				},
			}, nil
		},
	})
}

func lookupCmd() string {
	exe, err := os.Executable()
	if err != nil {
		panic(fmt.Errorf("error getting executable path: %w", err))
	}

	// Resolve any symlinks in the executable path so we don't bust the cache on every build.
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		panic(fmt.Errorf("error resolving symlink for executable path: %w", err))
	}
	return exe
}

func frontendCmd(args ...string) llb.RunOption {
	return llb.Args(append([]string{lookupCmd()}, args...))
}

func frontendMount(localSt *llb.State) llb.RunOption {
	p := lookupCmd()
	return llb.AddMount(p, *localSt, llb.Readonly, llb.SourcePath(p))
}

func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	res, handled, err := handleSubrequest(ctx, bc)
	if err != nil || handled {
		return res, err
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		spec, err := loadSpec(ctx, bc)
		if err != nil {
			return nil, nil, err
		}

		switch bc.Target {
		case targetBuildroot, "":
			return handleBuildRoot(ctx, client, spec)
		case targetResolve:
			return handleResolve(ctx, client, spec)
		case targetSpec:
			return handleSpec(ctx, client, spec)
		case targetRPM:
			return handleRPM(ctx, client, spec)
		case targetSources:
			return handleSources(ctx, client, spec)
		default:
			return nil, nil, fmt.Errorf("unknown target %q", bc.Target)
		}
	})
	if err != nil {
		return nil, err
	}
	return rb.Finalize()
}
