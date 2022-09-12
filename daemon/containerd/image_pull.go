package containerd

import (
	"context"
	"errors"
	"io"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	nyduslabel "github.com/containerd/nydus-snapshotter/pkg/label"
	stargzsource "github.com/containerd/stargz-snapshotter/fs/source"
	"github.com/docker/distribution"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/streamformatter"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// PullImage initiates a pull operation. image is the repository name to pull, and
// tagOrDigest may be either empty, or indicate a specific tag or digest to pull.
func (i *ImageService) PullImage(ctx context.Context, image, tagOrDigest string, platform *specs.Platform, metaHeaders map[string][]string, authConfig *registry.AuthConfig, outStream io.Writer) error {
	var opts []containerd.RemoteOpt
	if platform != nil {
		opts = append(opts, containerd.WithPlatform(platforms.Format(*platform)))
	}
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return errdefs.InvalidParameter(err)
	}

	// TODO(thaJeztah) this could use a WithTagOrDigest() utility
	if tagOrDigest != "" {
		// The "tag" could actually be a digest.
		var dgst digest.Digest
		dgst, err = digest.Parse(tagOrDigest)
		if err == nil {
			ref, err = reference.WithDigest(reference.TrimNamed(ref), dgst)
		} else {
			ref, err = reference.WithTag(ref, tagOrDigest)
		}
		if err != nil {
			return errdefs.InvalidParameter(err)
		}
	}

	resolver, _ := i.newResolverFromAuthConfig(authConfig)
	opts = append(opts, containerd.WithResolver(resolver))

	jobs := newJobs()
	h := images.HandlerFunc(func(ctx context.Context, desc specs.Descriptor) ([]specs.Descriptor, error) {
		if desc.MediaType != images.MediaTypeDockerSchema1Manifest {
			jobs.Add(desc)
		}
		return nil, nil
	})
	opts = append(opts, containerd.WithImageHandler(h))
	opts = i.applySnapshotterOpts(opts, ref)

	out := streamformatter.NewJSONProgressOutput(outStream, false)
	finishProgress := showProgress(ctx, jobs, out, pullProgress(i.client.ContentStore(), true))
	defer finishProgress()

	_, err = i.client.Pull(ctx, ref.String(), opts...)
	return err
}

// GetRepository returns a repository from the registry.
func (i *ImageService) GetRepository(ctx context.Context, ref reference.Named, authConfig *registry.AuthConfig) (distribution.Repository, error) {
	return nil, errors.New("not implemented")
}

func (i *ImageService) applySnapshotterOpts(opts []containerd.RemoteOpt, ref reference.Named) []containerd.RemoteOpt {
	opts = append(opts, containerd.WithPullUnpack)
	opts = append(opts, containerd.WithPullSnapshotter(i.snapshotter))

	var wrapper func(images.Handler) images.Handler
	switch i.snapshotter {
	case "stargz":
		const prefetch int64 = 10 * 1024 * 1024 // 10MiB
		wrapper = stargzsource.AppendDefaultLabelsHandlerWrapper(ref.String(), prefetch)
	case "nydus":
		wrapper = nyduslabel.AppendLabelsHandlerWrapper(ref.String())
	}
	if wrapper != nil {
		opts = append(opts, containerd.WithImageHandlerWrapper(wrapper))
	}
	return opts
}
