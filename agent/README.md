# GitOps Agent

The GitOps Agent leverages the GitOps Engine and provides access to many engine features via a simple CLI interface.
The agent provides the same set of core features as Argo CD including basic reconciliation, syncing as well as sync hooks and sync waves.

The main difference is that the agent is syncing one Git repository into the same cluster where it is installed.

## Quick Start

The agent supports two modes:

* full cluster mode - agent manages the whole cluster
* namespaced mode - agent manages the same namespace where it is installed

### Namespaced Mode

By default the agent is configured to use manifests from [guestbook](https://github.com/argoproj/argocd-example-apps/tree/master/guestbook)
directory in https://github.com/argoproj/argocd-example-apps repository.

Install the agent with the default settings using the command below. Done!

```bash
kubectl apply -f agent/manifests/install-namespaced.yaml
kubectl rollout status deploy/gitops-agent
```

The the agent logs:

```bash
kubectl logs -f deploy/gitops-agent gitops-agent
```

Find the guestbook deployment in the current K8S namespace:

```bash
kubectl get deployment
```

### Customize Git Repository

The agent runs [git-sync](https://github.com/kubernetes/git-sync) in as sidecar container to access the repository.
Update the container env [variables](https://github.com/kubernetes/git-sync#parameters) to change the repository.


### Demo Recording

[![asciicast](https://asciinema.org/a/FWbvVAiSsiI87wQx2TJbRMlxN.svg)](https://asciinema.org/a/FWbvVAiSsiI87wQx2TJbRMlxN)