package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
)

// buildOutputLimit is how much of the end of a build's output is kept.
const buildOutputLimit = 32 * 1024

// cpuPeriod is the CFS period build limits are expressed in.
const cpuPeriod = 100_000

func (m *manager) BuildProjectImage(ctx context.Context, tag string, dockerfile []byte, opts BuildOptions) (string, error) {
	// The build context is the Dockerfile alone, so nothing from the host
	// can be copied into the image.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(dockerfile)), Mode: 0644}); err != nil {
		return "", fmt.Errorf("writing Dockerfile tar header: %w", err)
	}
	if _, err := tw.Write(dockerfile); err != nil {
		return "", fmt.Errorf("writing Dockerfile tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("closing tar writer: %w", err)
	}

	// The classic builder applies NetworkMode and the resource limits to
	// each RUN step's container; BuildKit ignores them.
	resp, err := m.cli.ImageBuild(ctx, &buf, client.ImageBuildOptions{
		Tags:        []string{tag},
		Dockerfile:  "Dockerfile",
		Remove:      true,
		ForceRemove: true,
		NetworkMode: opts.NetworkMode,
		Memory:      opts.Memory,
		MemorySwap:  opts.Memory,
		CPUPeriod:   cpuPeriod,
		CPUQuota:    opts.NanoCPUs * cpuPeriod / 1_000_000_000,
		Labels:      opts.Labels,
		Version:     build.BuilderV1,
	})
	if err != nil {
		return "", fmt.Errorf("building image: %w", err)
	}
	defer resp.Body.Close()

	var output []byte
	keep := func(s string) {
		output = append(output, s...)
		if over := len(output) - buildOutputLimit; over > 0 {
			output = output[over:]
		}
	}
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream      string `json:"stream"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return string(output), nil
			}
			return string(output), fmt.Errorf("reading build output: %w", err)
		}
		keep(msg.Stream)
		if msg.Error != "" {
			reason := msg.ErrorDetail.Message
			if reason == "" {
				reason = msg.Error
			}
			return string(output), errors.New(reason)
		}
	}
}

func (m *manager) ImageID(ctx context.Context, ref string) (string, error) {
	inspect, err := m.cli.ImageInspect(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("inspecting image %s: %w", ref, err)
	}
	return inspect.ID, nil
}

func (m *manager) ListImages(ctx context.Context, label string) ([]ImageInfo, error) {
	res, err := m.cli.ImageList(ctx, client.ImageListOptions{Filters: make(client.Filters).Add("label", label)})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}
	images := make([]ImageInfo, 0, len(res.Items))
	for _, s := range res.Items {
		images = append(images, ImageInfo{ID: s.ID, Tags: s.RepoTags})
	}
	return images, nil
}

func (m *manager) RemoveImage(ctx context.Context, ref string) error {
	if _, err := m.cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{PruneChildren: true}); err != nil {
		return fmt.Errorf("removing image %s: %w", ref, err)
	}
	return nil
}
