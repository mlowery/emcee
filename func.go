package emcee

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/fatih/color"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	// pointer since Mutex cannot be copied
	logMutex = &sync.Mutex{}

	colorWheel = []*color.Color{
		color.New(color.FgRed),
		color.New(color.FgGreen),
		color.New(color.FgYellow),
		color.New(color.FgBlue),
		color.New(color.FgMagenta),
		color.New(color.FgCyan),
	}

	colorIndex = 0

	CommandFuncOutputOptions = []string{
		CommandFuncOutputPlain,
		CommandFuncOutputColor,
		CommandFuncOutputPrefix,
		CommandFuncOutputNone,
	}
)

const (
	CommandFuncOutputPlain  = "plain"
	CommandFuncOutputColor  = "color"
	CommandFuncOutputPrefix = "prefix"
	CommandFuncOutputNone   = "none"
)

func NewCommandFunc(outputMode string, name string, arg ...string) DoInClusterFunc {
	return func(config *NamedRestConfig) error {
		kubeconfig, err := restConfigToTempKubeconfig(config)
		if err != nil {
			return fmt.Errorf("failed to convert rest config to kubeconfig: %w", err)
		}
		defer os.Remove(kubeconfig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, arg...)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("KUBECONFIG=%s", kubeconfig),
		)

		// lock ensures no interleaving of output and protects colorIndex
		logMutex.Lock()
		defer logMutex.Unlock()
		printf := func(format string, a ...interface{}) {
			fmt.Printf(format, a...)
		}
		switch outputMode {
		case CommandFuncOutputNone:
			printf = func(format string, a ...interface{}) {}
		case CommandFuncOutputColor:
			if colorIndex == len(colorWheel)-1 {
				colorIndex = 0
			}
			colorPrintf := colorWheel[colorIndex].PrintfFunc()
			colorIndex += 1
			linePrefix := fmt.Sprintf("%10s|", config.ConfigName)
			printf = func(format string, a ...interface{}) {
				colorPrintf(fmt.Sprintf("%s%s", linePrefix, format), a...)
			}

		case CommandFuncOutputPrefix:
			linePrefix := fmt.Sprintf("%10s|", config.ConfigName)
			printf = func(format string, a ...interface{}) {
				fmt.Printf(fmt.Sprintf("%s%s", linePrefix, format), a...)
			}
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to get pipe for command %q: %w", name, err)
		}
		cmd.Stderr = cmd.Stdout
		cmd.Start()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			m := scanner.Text()
			printf("%s\n", m)
		}

		err = cmd.Wait()
		if err != nil {
			return fmt.Errorf("failed to run command %q: %w", name, err)
		}
		return nil
	}
}

func restConfigToTempKubeconfig(config *NamedRestConfig) (string, error) {
	rawConfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{"defaultCluster": {
			Server:                config.Host,
			InsecureSkipTLSVerify: true,
		}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"defaultAuthInfo": {
			Token: config.BearerToken,
			// this is for if exec is being used
			Exec: config.ExecProvider.DeepCopy(),
		}},
		Contexts: map[string]*clientcmdapi.Context{config.ConfigName: {
			Cluster:  "defaultCluster",
			AuthInfo: "defaultAuthInfo",
		}},
		CurrentContext: config.ConfigName,
	}

	tmpFile, err := ioutil.TempFile(os.TempDir(), "emcee-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}

	err = clientcmd.WriteToFile(rawConfig, tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return tmpFile.Name(), nil
}
