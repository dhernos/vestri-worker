package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const dockerInspectTimeout = 30 * time.Second

type composePSItem struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	Image   string `json:"Image"`
}

type stackImageStatusService struct {
	Service          string `json:"service"`
	Image            string `json:"image"`
	ContainerID      string `json:"containerId,omitempty"`
	ContainerImageID string `json:"containerImageId,omitempty"`
	LocalImageID     string `json:"localImageId,omitempty"`
	LocalRepoDigest  string `json:"localRepoDigest,omitempty"`
	RemoteDigest     string `json:"remoteDigest,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
}

type stackImageStatusResponse struct {
	UpdateAvailable bool                      `json:"updateAvailable"`
	Services        []stackImageStatusService `json:"services"`
}

func StackImageStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, stackName, err := parseStackTarget(r, false)
	if err != nil {
		logStackOpError(r, "image-status", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	status, err := collectStackImageStatus(stackPath)
	if err != nil {
		logStackOpError(r, "image-status", stackName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
	logStackOp(r, "image-status", stackName)
}

func collectStackImageStatus(stackPath string) (stackImageStatusResponse, error) {
	out, err := RunCompose(stackPath, "ps", "-a", "--format", "json")
	if err != nil {
		return stackImageStatusResponse{}, fmt.Errorf("failed to list compose services: %w (%s)", err, strings.TrimSpace(out))
	}

	items, err := parseComposePSOutput(out)
	if err != nil {
		return stackImageStatusResponse{}, err
	}

	servicesByName := make(map[string]*stackImageStatusService)
	localImageIDCache := make(map[string]string)
	localRepoDigestCache := make(map[string]string)
	remoteDigestCache := make(map[string]string)

	resolveLocalImageID := func(imageName string) string {
		if value, found := localImageIDCache[imageName]; found {
			return value
		}
		value, err := inspectLocalImageID(imageName)
		if err != nil {
			localImageIDCache[imageName] = ""
			return ""
		}
		localImageIDCache[imageName] = value
		return value
	}

	resolveLocalRepoDigest := func(imageName string) string {
		if value, found := localRepoDigestCache[imageName]; found {
			return value
		}
		value, err := inspectLocalRepoDigest(imageName)
		if err != nil {
			localRepoDigestCache[imageName] = ""
			return ""
		}
		localRepoDigestCache[imageName] = value
		return value
	}

	resolveRemoteDigest := func(imageName string) string {
		if value, found := remoteDigestCache[imageName]; found {
			return value
		}
		value, err := inspectRemoteImageDigest(imageName)
		if err != nil {
			remoteDigestCache[imageName] = ""
			return ""
		}
		remoteDigestCache[imageName] = value
		return value
	}

	for _, item := range items {
		serviceName := strings.TrimSpace(item.Service)
		imageName := strings.TrimSpace(item.Image)
		if serviceName == "" || imageName == "" {
			continue
		}

		entry, found := servicesByName[serviceName]
		if !found {
			entry = &stackImageStatusService{
				Service: serviceName,
				Image:   imageName,
			}
			servicesByName[serviceName] = entry
		} else if entry.Image == "" {
			entry.Image = imageName
		}

		containerRef := strings.TrimSpace(item.ID)
		if containerRef == "" {
			containerRef = strings.TrimSpace(item.Name)
		}
		if containerRef != "" && entry.ContainerID == "" {
			entry.ContainerID = containerRef
		}

		containerImageID := ""
		if containerRef != "" {
			containerImageID, err = inspectContainerImageID(containerRef)
		}
		if containerImageID != "" && entry.ContainerImageID == "" {
			entry.ContainerImageID = containerImageID
		}

		localImageID := resolveLocalImageID(imageName)
		if localImageID != "" && entry.LocalImageID == "" {
			entry.LocalImageID = localImageID
		}

		localRepoDigest := resolveLocalRepoDigest(imageName)
		if localRepoDigest != "" && entry.LocalRepoDigest == "" {
			entry.LocalRepoDigest = localRepoDigest
		}

		remoteDigest := resolveRemoteDigest(imageName)
		if remoteDigest != "" && entry.RemoteDigest == "" {
			entry.RemoteDigest = remoteDigest
		}

		// 1) If the running container image differs from the local tagged image,
		// an update was already pulled and is pending a restart.
		if containerImageID != "" && localImageID != "" && containerImageID != localImageID {
			entry.UpdateAvailable = true
		}
		// 2) If the remote registry digest differs from the local digest,
		// a newer image exists remotely (even before pull).
		if remoteDigest != "" && localRepoDigest != "" && remoteDigest != localRepoDigest {
			entry.UpdateAvailable = true
		}
	}

	serviceNames := make([]string, 0, len(servicesByName))
	for serviceName := range servicesByName {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)

	resp := stackImageStatusResponse{
		Services: make([]stackImageStatusService, 0, len(serviceNames)),
	}
	for _, serviceName := range serviceNames {
		entry := servicesByName[serviceName]
		if entry.UpdateAvailable {
			resp.UpdateAvailable = true
		}
		resp.Services = append(resp.Services, *entry)
	}
	return resp, nil
}

func parseComposePSOutput(output string) ([]composePSItem, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var items []composePSItem
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("failed to parse compose ps json output: %w", err)
		}
		return items, nil
	}

	lines := strings.Split(trimmed, "\n")
	items := make([]composePSItem, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item composePSItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("failed to parse compose ps json line: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

func inspectContainerImageID(containerRef string) (string, error) {
	containerRef = strings.TrimSpace(containerRef)
	if containerRef == "" {
		return "", fmt.Errorf("container reference is required")
	}
	return runDockerCommand("inspect", "--format", "{{.Image}}", containerRef)
}

func inspectLocalImageID(imageName string) (string, error) {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return "", fmt.Errorf("image name is required")
	}
	return runDockerCommand("image", "inspect", "--format", "{{.Id}}", imageName)
}

func inspectLocalRepoDigest(imageName string) (string, error) {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return "", fmt.Errorf("image name is required")
	}

	output, err := runDockerCommand("image", "inspect", "--format", "{{json .RepoDigests}}", imageName)
	if err != nil {
		return "", err
	}

	var repoDigests []string
	if err := json.Unmarshal([]byte(output), &repoDigests); err != nil {
		return "", fmt.Errorf("failed to parse local repo digests: %w", err)
	}

	for _, repoDigest := range repoDigests {
		if digest := digestFromRepoDigest(repoDigest); digest != "" {
			return digest, nil
		}
	}
	return "", fmt.Errorf("no local repo digest available for image")
}

func inspectRemoteImageDigest(imageName string) (string, error) {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return "", fmt.Errorf("image name is required")
	}

	output, err := runDockerCommand("manifest", "inspect", "--verbose", imageName)
	if err != nil {
		return "", err
	}

	digest, err := parseManifestInspectDigest(output)
	if err != nil {
		return "", err
	}
	return digest, nil
}

type manifestInspectVerbose struct {
	Descriptor struct {
		Digest string `json:"digest"`
	} `json:"Descriptor"`
	Digest string `json:"Digest"`
}

func parseManifestInspectDigest(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "", fmt.Errorf("empty manifest inspect output")
	}

	var single manifestInspectVerbose
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil {
		if digest := normalizeDigest(single.Descriptor.Digest); digest != "" {
			return digest, nil
		}
		if digest := normalizeDigest(single.Digest); digest != "" {
			return digest, nil
		}
	}

	var list []manifestInspectVerbose
	if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
		for _, item := range list {
			if digest := normalizeDigest(item.Descriptor.Digest); digest != "" {
				return digest, nil
			}
			if digest := normalizeDigest(item.Digest); digest != "" {
				return digest, nil
			}
		}
	}

	return "", fmt.Errorf("no remote digest found in manifest inspect output")
}

func digestFromRepoDigest(repoDigest string) string {
	parts := strings.SplitN(strings.TrimSpace(repoDigest), "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return normalizeDigest(parts[1])
}

func normalizeDigest(raw string) string {
	digest := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(digest, "sha256:") {
		return digest
	}
	return ""
}

func runDockerCommand(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerInspectTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker command failed: %w (%s)", err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
