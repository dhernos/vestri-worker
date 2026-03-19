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
		if containerRef == "" {
			continue
		}
		if entry.ContainerID == "" {
			entry.ContainerID = containerRef
		}

		containerImageID, err := inspectContainerImageID(containerRef)
		if err == nil && entry.ContainerImageID == "" {
			entry.ContainerImageID = containerImageID
		}

		localImageID, err := inspectLocalImageID(imageName)
		if err == nil && entry.LocalImageID == "" {
			entry.LocalImageID = localImageID
		}

		if containerImageID != "" && localImageID != "" && containerImageID != localImageID {
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
