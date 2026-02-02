package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"
)

const (
	kustomizeBasePath    = "kustomize/op-acceptor"
	jobsPath             = "jobs"
	componentsPath       = "components"
	basesPath            = "bases"
	tempKustomizationDir = ".run"
	acceptorJobName      = "op-acceptor"
	acceptorJobNamespace = "op-acceptor-jobs"
)

// networkDirs are the directories to search for network job components
var networkDirs = []string{
	"oplabs-dev-client",
	"oplabs-dev-infra",
}

// RunRemoteCommand defines the "run-remote" command for running acceptance tests on a remote k8s cluster.
func RunRemoteCommand() *cli.Command {
	return &cli.Command{
		Name:      "run-remote",
		Usage:     "Run acceptance tests on a remote Kubernetes cluster",
		ArgsUsage: "<network> <gate>",
		Description: `Runs an acceptance test job on a remote Kubernetes cluster by combining:
  - Base resources (job, secrets, RBAC, etc.)
  - Network component (devnet configuration, cluster info)
  - Gate component (test gate to run)

The command generates a temporary kustomization, builds it, deletes any existing
job, and applies the new configuration.

Examples:
  op-acceptor run-remote eris isthmus
  op-acceptor run-remote bench-geth-0 base
  op-acceptor run-remote --list-networks
  op-acceptor run-remote --list-gates`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "k8s-repo-path",
				Usage:   "path to the k8s repository containing kustomize/op-acceptor",
				EnvVars: []string{"K8S_REPO_PATH"},
			},
			&cli.BoolFlag{
				Name:  "list-networks",
				Usage: "list available network components",
			},
			&cli.BoolFlag{
				Name:  "list-gates",
				Usage: "list available gate components",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "generate and print the manifest without applying",
			},
			&cli.BoolFlag{
				Name:  "no-delete",
				Usage: "don't delete existing job before applying (will fail if job exists)",
			},
			&cli.BoolFlag{
				Name:    "tail-logs",
				Aliases: []string{"f"},
				Usage:   "tail the job logs after deploying",
			},
		},
		Action: runRemoteAction,
	}
}

func runRemoteAction(c *cli.Context) error {
	k8sRepoPath := c.String("k8s-repo-path")
	if k8sRepoPath == "" {
		return fmt.Errorf("--k8s-repo-path or K8S_REPO_PATH environment variable is required")
	}

	// Verify the k8s repo path exists and has the expected structure
	opAcceptorPath := filepath.Join(k8sRepoPath, kustomizeBasePath)
	jobsBasePath := filepath.Join(opAcceptorPath, jobsPath)
	if _, err := os.Stat(jobsBasePath); os.IsNotExist(err) {
		return fmt.Errorf("k8s repo path does not contain %s/%s: %s", kustomizeBasePath, jobsPath, k8sRepoPath)
	}

	// Handle list commands
	if c.Bool("list-networks") {
		return listNetworks(opAcceptorPath)
	}
	if c.Bool("list-gates") {
		return listJobComponents(jobsBasePath, "gates")
	}

	// Require network and gate arguments
	if c.NArg() < 2 {
		return fmt.Errorf("requires <network> and <gate> arguments\n\nUsage: op-acceptor run-remote <network> <gate>\n\nUse --list-networks and --list-gates to see available options")
	}

	network := c.Args().Get(0)
	gate := c.Args().Get(1)

	// Find the network component path
	networkComponentPath, err := findNetworkComponent(opAcceptorPath, network)
	if err != nil {
		return err
	}

	// Validate gate component
	if err := validateJobComponent(jobsBasePath, "gates", gate); err != nil {
		return err
	}

	log.Info("running remote acceptance tests",
		"network", network,
		"gate", gate,
		"k8s_repo", k8sRepoPath,
	)

	// Generate temporary kustomization
	tmpDir := filepath.Join(jobsBasePath, tempKustomizationDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Calculate relative paths from tmpDir
	relNetworkPath, err := filepath.Rel(tmpDir, networkComponentPath)
	if err != nil {
		return fmt.Errorf("calculate relative path for network: %w", err)
	}

	kustomizationContent := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - ../%s

components:
  - %s
  - ../%s/gates/%s
`, basesPath, relNetworkPath, componentsPath, gate)

	kustomizationFile := filepath.Join(tmpDir, "kustomization.yaml")
	if err := os.WriteFile(kustomizationFile, []byte(kustomizationContent), 0644); err != nil {
		return fmt.Errorf("write kustomization: %w", err)
	}

	log.Debug("generated kustomization", "path", kustomizationFile, "content", kustomizationContent)

	// Build the kustomization
	// Use --load-restrictor to allow loading files from parent directories (needed for devnet-env.json)
	log.Info("building kustomize manifest...")
	buildCmd := exec.Command("kustomize", "build", "--load-restrictor", "LoadRestrictionsNone", tmpDir)
	manifest, err := buildCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("kustomize build failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("kustomize build: %w", err)
	}

	if c.Bool("dry-run") {
		fmt.Println(string(manifest))
		return nil
	}

	// Delete existing job (ignore errors if it doesn't exist)
	if !c.Bool("no-delete") {
		log.Info("deleting existing job...",
			"job", acceptorJobName,
			"namespace", acceptorJobNamespace,
		)
		deleteCmd := exec.Command("kubectl", "delete", "job", acceptorJobName,
			"-n", acceptorJobNamespace,
			"--ignore-not-found",
			"--wait=false",
		)
		if output, err := deleteCmd.CombinedOutput(); err != nil {
			log.Warn("failed to delete existing job", "error", err, "output", string(output))
		}
	}

	// Apply the manifest
	log.Info("applying job manifest...")
	applyCmd := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd.Stdin = bytes.NewReader(manifest)
	applyOutput, err := applyCmd.CombinedOutput()
	if err != nil {
		outputStr := string(applyOutput)
		// Check for image not found errors from admission webhooks
		if strings.Contains(outputStr, "MANIFEST_UNKNOWN") ||
			strings.Contains(outputStr, "could not get digest") ||
			strings.Contains(outputStr, "Failed to fetch") {
			// Extract the image tag from the error message if possible
			imageHint := extractImageFromError(outputStr)
			return fmt.Errorf("kubectl apply failed: image not found in registry\n\n%s\n\n"+
				"The container image does not exist. Please build and push it first:\n"+
				"  cd /path/to/infra\n"+
				"  docker build -f op-acceptor/Dockerfile -t %s .\n"+
				"  docker push %s\n",
				outputStr, imageHint, imageHint)
		}
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, outputStr)
	}

	log.Info("job applied successfully",
		"network", network,
		"gate", gate,
	)
	fmt.Println(string(applyOutput))

	if c.Bool("tail-logs") {
		return tailJobLogs()
	}

	// Print helpful commands
	fmt.Printf("\nTo watch the job:\n")
	fmt.Printf("  kubectl logs -f job/%s -n %s\n", acceptorJobName, acceptorJobNamespace)
	fmt.Printf("\nTo check job status:\n")
	fmt.Printf("  kubectl get job %s -n %s\n", acceptorJobName, acceptorJobNamespace)

	return nil
}

// tailJobLogs waits for the job pod to be ready and then tails the logs.
func tailJobLogs() error {
	log.Info("waiting for job pod to be ready...")

	// Wait for the pod to exist and be running
	waitCmd := exec.Command("kubectl", "wait",
		"--for=condition=Ready",
		"pod",
		"-l", fmt.Sprintf("job-name=%s", acceptorJobName),
		"-n", acceptorJobNamespace,
		"--timeout=300s",
	)
	if output, err := waitCmd.CombinedOutput(); err != nil {
		// Pod might have already completed or failed, try to get logs anyway
		log.Warn("pod wait returned error (may have completed)", "output", string(output))
	}

	log.Info("tailing job logs...", "job", acceptorJobName, "namespace", acceptorJobNamespace)
	fmt.Println("---")

	// Tail logs from the job (follows until completion)
	logsCmd := exec.Command("kubectl", "logs",
		"-f",
		fmt.Sprintf("job/%s", acceptorJobName),
		"-n", acceptorJobNamespace,
		"-c", "op-acceptor", // Only tail the main container, not the artifact uploader
	)
	logsCmd.Stdout = os.Stdout
	logsCmd.Stderr = os.Stderr

	if err := logsCmd.Run(); err != nil {
		// Check if job completed successfully
		statusCmd := exec.Command("kubectl", "get", "job", acceptorJobName,
			"-n", acceptorJobNamespace,
			"-o", "jsonpath={.status.succeeded}",
		)
		if output, _ := statusCmd.Output(); string(output) == "1" {
			log.Info("job completed successfully")
			return nil
		}
		return fmt.Errorf("failed to tail logs: %w", err)
	}

	return nil
}

// findNetworkComponent finds the network job component path.
// Networks are located at oplabs-dev-client/<network>-op-acceptor/job/ or
// oplabs-dev-infra/<network>-op-acceptor/job/.
func findNetworkComponent(opAcceptorPath, network string) (string, error) {
	// Try each network directory
	for _, dir := range networkDirs {
		// Network directories are named <network>-op-acceptor
		networkDir := fmt.Sprintf("%s-op-acceptor", network)
		jobPath := filepath.Join(opAcceptorPath, dir, networkDir, "job", "kustomization.yaml")
		if _, err := os.Stat(jobPath); err == nil {
			return filepath.Dir(jobPath), nil
		}
	}

	// List available networks for error message
	networks, _ := getAvailableNetworks(opAcceptorPath)
	return "", fmt.Errorf("network %q not found. Available networks: %s",
		network, strings.Join(networks, ", "))
}

// getAvailableNetworks returns all available network names.
func getAvailableNetworks(opAcceptorPath string) ([]string, error) {
	var networks []string

	for _, dir := range networkDirs {
		networkParentDir := filepath.Join(opAcceptorPath, dir)
		entries, err := os.ReadDir(networkParentDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			// Check if this network has a job component (job/kustomization.yaml)
			jobPath := filepath.Join(networkParentDir, entry.Name(), "job", "kustomization.yaml")
			if _, err := os.Stat(jobPath); err == nil {
				// Extract network name (remove -op-acceptor suffix)
				name := strings.TrimSuffix(entry.Name(), "-op-acceptor")
				networks = append(networks, name)
			}
		}
	}

	return networks, nil
}

// listNetworks prints available networks to stdout.
func listNetworks(opAcceptorPath string) error {
	networks, err := getAvailableNetworks(opAcceptorPath)
	if err != nil {
		return fmt.Errorf("read networks: %w", err)
	}

	fmt.Printf("Available networks:\n")
	for _, name := range networks {
		fmt.Printf("  - %s\n", name)
	}
	return nil
}

// listJobComponents prints available job components of a given type to stdout.
func listJobComponents(jobsBasePath, componentType string) error {
	components, err := getAvailableJobComponents(jobsBasePath, componentType)
	if err != nil {
		return fmt.Errorf("read %s components: %w", componentType, err)
	}

	fmt.Printf("Available %s:\n", componentType)
	for _, name := range components {
		fmt.Printf("  - %s\n", name)
	}
	return nil
}

// validateJobComponent checks that a job component exists.
func validateJobComponent(jobsBasePath, componentType, name string) error {
	componentPath := filepath.Join(jobsBasePath, componentsPath, componentType, name, "kustomization.yaml")
	if _, err := os.Stat(componentPath); os.IsNotExist(err) {
		available, _ := getAvailableJobComponents(jobsBasePath, componentType)
		return fmt.Errorf("%s %q not found. Available %s: %s",
			strings.TrimSuffix(componentType, "s"), name, componentType, strings.Join(available, ", "))
	}
	return nil
}

// getAvailableJobComponents returns a list of available job component names.
func getAvailableJobComponents(jobsBasePath, componentType string) ([]string, error) {
	componentsDir := filepath.Join(jobsBasePath, componentsPath, componentType)
	entries, err := os.ReadDir(componentsDir)
	if err != nil {
		return nil, err
	}

	var components []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		kustomizationPath := filepath.Join(componentsDir, entry.Name(), "kustomization.yaml")
		if _, err := os.Stat(kustomizationPath); err == nil {
			components = append(components, entry.Name())
		}
	}
	return components, nil
}

// extractImageFromError attempts to extract the image reference from an error message.
// Returns a placeholder if it can't find a specific image.
func extractImageFromError(errOutput string) string {
	// Look for patterns like "us-docker.pkg.dev/.../op-acceptor:tag"
	// The error typically contains the full image reference
	patterns := []string{
		`us-docker\.pkg\.dev/[^:]+:[^\s"']+`,
		`gcr\.io/[^:]+:[^\s"']+`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindString(errOutput); match != "" {
			return match
		}
	}

	return "us-docker.pkg.dev/oplabs-tools-artifacts/images/op-acceptor:<your-tag>"
}
