package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v2"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"

	"github.com/mlowery/emcee"
)

func main() {
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("failed to get current user: %v", err)
	}
	defaultKubeconfig := filepath.Join(usr.HomeDir, ".kube/config")

	app := &cli.App{
		Name:  "🎤emcee",
		Usage: "Multi-cluster operations",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "Run command against given clusters.",
				Action: func(c *cli.Context) error {
					var cmd string
					var args []string
					for _, arg := range c.Args().Slice() {
						if cmd == "" {
							cmd = arg
							continue
						}
						if cmd != "" {
							args = append(args, arg)
						}
					}
					if cmd == "" {
						log.Fatalf("command is required")
					}

					var contexts []string
					contextList := c.StringSlice("context")
					contextFile := c.String("context-file")
					if len(contextList) > 0 && len(contextFile) > 0 {
						log.Fatalf("--context and --context-file are mutually exclusive")
					}
					if len(contextList) > 0 {
						contexts = contextList
					} else if len(contextFile) > 0 {
						file, err := os.Open(contextFile)
						if err != nil {
							log.Fatalf("failed to open context-file: %v", err)
						}
						defer file.Close()

						scanner := bufio.NewScanner(file)
						for scanner.Scan() {
							contexts = append(contexts, scanner.Text())
						}

						if err := scanner.Err(); err != nil {
							log.Fatalf("failed to read context-file: %v", err)
						}
					}

					kubeconfig := c.String("kubeconfig")
					crContext := c.String("cr-context")
					var getter emcee.RestConfigGetter
					if len(contexts) > 0 {
						getter = emcee.NewKubeContextGetter(kubeconfig, contexts)
					} else if len(crContext) > 0 {
						selector := c.String("selector")
						if len(selector) == 0 {
							log.Println("WARNING: running without a selector")
						}
						fn := emcee.ClusterNameFunc
						if crLabel := c.String("cr-label"); len(crLabel) != 0 {
							fn = emcee.MakeClusterLabelFunc(crLabel)
						}
						getter = emcee.NewCRGetter(kubeconfig, crContext, selector, c.String("cr-namespace"), fn)
					} else {
						log.Fatalf("one of context or cr-context is required")
					}
					restConfigs, err := getter.Get()
					if err != nil {
						log.Fatalf("failed to get rest configs: %v", err)
					}
					log.Printf("processing %d context(s)\n", len(restConfigs))
					runner := emcee.NewRunner(c.Int("workers"), restConfigs, emcee.NewCommandFunc(c.String("output"), cmd, args...))
					sigCh := make(chan os.Signal)
					stopCh := make(chan struct{})
					signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
					go func() {
						<-sigCh
						log.Println("got signal to shut down")
						close(stopCh)
					}()
					err = runner.Run(stopCh)
					if err != nil {
						errs := multierr.Errors(err)
						var buffer bytes.Buffer
						for i, err := range errs {
							buffer.WriteString(fmt.Sprintf("[error %3d]: %v\n", i+1, err))
						}
						log.Fatalf("failed to run (%d errors):\n%s", len(errs), buffer.String())
					}
					return nil
				},
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "kubeconfig", Value: defaultKubeconfig, Usage: "path to kubeconfig"},
					&cli.StringSliceFlag{Name: "context", Aliases: []string{"c"}, Usage: "context from kubeconfig (can be repeated)"},
					&cli.StringFlag{Name: "context-file", Usage: "file containing contexts, one per line"},
					&cli.StringFlag{Name: "cr-context", Usage: "context from kubeconfig pointing to cluster registry"},
					&cli.StringFlag{Name: "cr-namespace", Value: v1.NamespaceDefault, Usage: "namespace within cluster registry to search for clusters"},
					&cli.StringFlag{Name: "cr-label", Usage: "optional label from cluster object to use as kubeconfig context name"},
					&cli.StringFlag{Name: "selector", Aliases: []string{"l"}, Value: "", Usage: "label selector for cluster registry"},
					&cli.IntFlag{Name: "workers", Aliases: []string{"w"}, Value: 1, Usage: "level of parallelism"},
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: emcee.CommandFuncOutputColor, Usage: fmt.Sprintf("output type (one of %s)", strings.Join(emcee.CommandFuncOutputOptions, ","))},
				},
			},
		},
		CommandNotFound: func(c *cli.Context, command string) {
			fmt.Fprintf(c.App.Writer, "unknown command: %q\n", command)
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
