# 🎤emcee

Multi-cluster (aka mc aka emcee) binary and library for running commands in parallel across Kubernetes clusters.

## Features

* Configurable parallelism
* Color-coded output for easy delineation of output per kubeconfig context

## Warning

Due to the parallelism of this tool, it is not recommended for mutating calls.

## Definitions

For the purpose of this project, kubeconfig "context," "cluster," and "restconfig" may be used interchangeably.

## Using

Your first decision is what command you want to run against all specified kubeconfig contexts.

After that, you need to decide how you want to derive the list of kubeconfig contexts. The method of deriving the kubeconfig contexts depends on which "REST config getter" you use. There are two out of the box: 

1. Explicit Contexts: With this, you just specify one or more contexts with `-c`.
2. Cluster Registry: With this, you specify the CR context with `--cr-context` and an optional selector to filter the clusters with `-l`.

### With Explicit Contexts

Run `auth can-i` in three contexts at once and print answer.

```sh
$ emcee run -c c1 -c c2 -c c3 -- kubectl auth can-i create secret -n default
        c1|yes
        c2|yes
        c3|yes
```

Here's another but calling the example script in this project that has no stdout but saves content to per-context files.

```
$ emcee run -c c1 -c c2 -c c3 -o none -- bash -c "example/run.sh"
$ ls *-out
```

### With Cluster Registry

Use of this method assumes you have [cluster registry](https://github.com/kubernetes/cluster-registry) running.

Search a configmap's data in all clusters matching selector `region=us-east-1` in cluster registry, up to `50` at once.

```sh
$ emcee run -w 50 --cr-context=cr -l region=us-east-1 -- bash -c "kubectl -n kube-system get cm mycm -o yaml | grep mykey || true"
```

### Adding Your Own Getter

Just implement `RestConfigGetter`.
