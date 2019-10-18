# GitOps Engine Design - Black Box

## Summary

During the elaboration of [White box](./design-white-box.md) option, it was discovered that some components are similar at a high-level but still have a lot of differences in 
design and implementation. This is not surprising because code was developed by different teams and with a focus on different use-cases. Given that it would require a lot of
effort to resolve such differences it is proposed contributing missing features of one project into an engine of another and use that engine in both projects.

## Proposal

It is proposed to use Argo CD application controller as the base for the GitOps engine and contribute a set of Flux features into it. There are two main reasons to try using Argo CD as
a base:
- Argo CD uses the _Application_ abstraction to represent the desired state and target the Kubernetes cluster. This abstraction works for both Argo CD and Flux.
- The Argo CD controller leverages Kubernetes watch APIs instead of polling. This enables Argo CD features such as Health assessment, UI and could provide better performance to
Flux as well.

The following Flux features are missing in Argo CD:

- Manifest generation using .flux.yaml files.
- GPG commit signatures verification - an ability to verify the commit signature before pushing changes to the Kubernetes.
- Namespace mode - an ability to control only given namespace in the target cluster. Currently, Argo CD requires read access in all namespaces.

These features must be contributed to Argo-Flux GitOps engine implementation before Flux starts using it.

Flux additionally provides the ability to monitor Docker registry and automatically push changes to the Git repository when a new image is released. Both teams feel the this should not
be a part of GitOps engine. So it is proposed to keep the feature only in Flux for now and then work together to move it into a separate component that would work for both Flux
and Argo CD.

### Differences in Core Functionality

Even core functionality of Argo CD has some differences. These differences might look minor but affect production systems and cannot be ignored. It is proposed to document all such
differences, document and propose a resolution. Following three categories are proposed:

- `Feature`. The difference is justified by a real business requirement and affects the user. To resolve it we should contribute a feature into GitOps engine.
- `Mode`. The is no real business requirement and difference exists because slightly different design decision was made during Argo CD/Flux implementation. To resolve it
we would have to introduce a setting that switches Engine between Argo CD/Flux mode. Going forward we should try to pick one behavior, deprecate the previous one and eventually migrate
all users to one behavior.
- `Random`. The difference is caused by an internal implementation detail and does not affect the user in any way. We can just ignore the difference.

**Differences List (WIP)**:

- `Feature` Policy annotation `fluxcd.io/ignore`. If either resource manifest in Git or live resource in the target cluster has an `fluxcd.io/ignore` then Flux doesn't touch it.
Argo CD does not have a similar annotation.

- `Feature` Garbage collection/resource pruning. Garbage collection logic in Argo CD and Flux is slightly different. Argo CD inject label `app.kubernetes.io/instance: <appName>`
to each resource and remove such resources during next syncing if they are no longer in Git. Flux inject label `fluxcd.io/sync-gc-mark -> sha256.<checksum>`  where `<sha>` is a
sha256 of the Git repo url, branch name, and paths and uses that label to identify if the resource can be garbage collected after it is no longer in Git. So if repo/branch and paths
change then Flux won't delete resources deployed before the change. Additionally, Flux applies `fluxcd.io/sync-checksum` annotation. The annotation is also used to prevent
accidental pruning.

- `Random` Apply Order Both Argo CD and Flux execute tasks in a predefined order. The order is slightly different:
  - Argo CD: https://github.com/argoproj/argo-cd/blob/4cb84b37ce4ac9e91b242a2250e8425d4977a961/controller/sync_tasks.go#L22
  - Flux: https://github.com/fluxcd/flux/blob/c8484d4553a674d54b74e61d974537799459e44e/pkg/cluster/kubernetes/sync.go#L433
The difference is very minor. I don't think it will affect the user and we can just ignore it.

- `Feature` Delete Order. Flux deletes resources in the reverse order, Argo CD does not.

### Hypothesis and assumptions

The proposed solution is based on the assumption that despite implementation differences the core functionality of Argo CD and Flux behaves in the same way. Both projects
ultimately extract the set of manifests from Git and use "kubectl apply" to change the cluster state. The minor differences are expected but we can resolve them by introducing new
knobs.

Also, the proposed approach is based on the assumption that Argo CD engine is flexible enough to cover all Flux use-cases, reproduce Flux's behavior with minor differences and can be easily integrated into Argo CD.

However, there is a risk that there will be too many differences and it might be not feasible to support all of them. To get early feedback, we will start with a Proof-of-Concept 
(PoC from now on) implementation which will serve as an experiment to assess the feasibility of the approach.

### Acceptance criteria

To consider the PoC successful (and with the exception of features excluded from the PoC to save time), 
all the following must hold true:
1. All the Flux unit and end-to-end tests must pass. The existing tests are limited, so we may decide to include additional ones.
2. The UX of Flux must remain unchanged. That includes:
   - The flags of `fluxd` and `fluxctl` must  be respected and can be used in the same way as before
     resulting in the same configuration behavioural changes.
   - Flux's API must remain unchanged. In particular, the web API (used by fluxctl) and the websocket API (e.g. used to 
     communicate with Weave Cloud) must work without changes.
3. Flux's writing behaviour on Git and Kubernetes must be identical. In particular:
   - Flux+GitEngine should make changes in Git if and only if Flux without GitEngine would had done it,
     in the same way (same content) and in the same situations
   - Flux+GitEngine should add and update Kubernetes resources if and only if Flux without GitEngine would had done, 
     in the same way (same content) and in the same situations

Unfortunately, there isn't a straightforward way to decidedly check for (3).

Additionally, there must be a clear way forward (in the shape of well-defined steps) 
for the features not covered by the PoC to work (complying with the points above) int he final GitOps  
Engine.

### GitOps Engine PoC

The PoC deliverables are:

- All PoC changes are in separate branches.
- Argo CD controller will be moved to https://github.com/argoproj/gitops-engine.
- Flux will import GitOps engine component from the https://github.com/argoproj/gitops-engine repository and use it to perform cluster state syncing.
- The flux installation and fluxctl behavior will remain the same other than using GitOps engine internally. That means there will be no creation of Application CRD or Argo CD
specific ConfigMaps/Secrets.
- For the sake of saving time POC does not include implementing features mentioned before. So no commit verification, only plain .yaml files support, and full cluster mode.

## Design Details

### Public API

To be documented during POC implementation.


## Alternatives

[White box](./design-white-box.md)
