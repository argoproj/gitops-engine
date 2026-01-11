/*
Package implements Kubernetes resources synchronization and provides the following main features:
  - basic syncing
  - resource pruning
  - resource hooks
  - sync waves
  - sync waves binary tree ordering
  - sync options

# Basic Syncing

Executes equivalent of `kubectl apply` for each specified resource. The apply operations are executed in the predefined
order depending of resource type: namespaces, custom resource definitions first and workload resources last.

# Resource Pruning

An ability to delete resources that no longer should exist in the cluster. By default obsolete resources are not deleted
and only reported in the sync operation result.

# Resource Hooks

Hooks provide an ability to create resources such as Pod, Job or any other resource, that are 'executed' before, after
or even during the synchronization process. Hooks enable use-cases such as database migration and post sync notifications.

Hooks are regular Kubernetes resources that have `argocd.argoproj.io/hook` annotation:

	apiVersion: batch/v1
	kind: Job
	metadata:
	  generateName: schema-migrate-
	  annotations:
	    argocd.argoproj.io/hook: PreSync

The annotation value indicates the sync operation phase:

  - PreSync - executes prior to the apply of the manifests.
  - PostSync - executes after all Sync hooks completed and were successful, a successful apply, and all resources in a Healthy state.
  - SyncFail - executes when the sync operation fails.
  - Sync - executes after all PreSync hooks completed and were successful, at the same time as the apply of the manifests.

Named hooks (i.e. ones with /metadata/name) will only be created once. If you want a hook to be re-created each time
either use BeforeHookCreation policy (see below) or /metadata/generateName.

The same resource hook might be executed in several sync phases:

	apiVersion: batch/v1
	kind: Job
	metadata:
	  generateName: schema-migrate-
	  annotations:
	    argocd.argoproj.io/hook: PreSync,PostSync

Hooks can be deleted in an automatic fashion using the annotation: argocd.argoproj.io/hook-delete-policy.

	apiVersion: batch/v1
	kind: Job
	metadata:
	  generateName: integration-test-
	  annotations:
	    argocd.argoproj.io/hook: PostSync
	    argocd.argoproj.io/hook-delete-policy: HookSucceeded

The following policies define when the hook will be deleted.

  - HookSucceeded - the hook resource is deleted after the hook succeeded (e.g. Job/Workflow completed successfully).
  - HookFailed - the hook resource is deleted after the hook failed.
  - BeforeHookCreation - any existing hook resource is deleted before the new one is created

# Sync Waves

The waves allow to group sync execution of syncing process into batches when each batch is executed sequentially one after
another. Hooks and resources are assigned to wave zero by default. The wave can be negative, so you can create a wave
that runs before all other resources. The `argocd.argoproj.io/sync-wave` annotation assign resource to a wave:

	metadata:
	  annotations:
	    argocd.argoproj.io/sync-wave: "5"

# Sync Waves Binary Tree Ordering

The wave ordering using a binary tree feature allows to run parallel waves of synchronisation where the sync-wave values
correspond to a complete binary tree with root's label equal to 1. A sync-wave value X would be considered less than Y
when using binary tree ordering if and only if there exists integers N and M such that :
Y = X * 2**N + M where 0 <= M < N.

The `argocd.argoproj.io/use-binary-tree-wave-ordering` annotation define the type of wave's ordering used for a resource's wave:

	metadata:
	  annotations:
	    argocd.argoproj.io/sync-wave: "5"
	    argocd.argoproj.io/use-binary-tree-wave-ordering: "true"

example of waves ordering using binary tree:

	1  ----->  2  ----->  4
	   \          \---->  5
	    \--->  3  ----->  6
	              \---->  7

Note that a resource using a binary tree ordering for sync waves will always be synced after all resources using a normal ordering.
Note also that all resources using a binary tree ordering and having a sync wave value inferior to 1 will behave like resources using
a normal wave ordering.

# Sync Options

The sync options allows customizing the synchronization of selected resources. The options are specified using the
annotation 'argocd.argoproj.io/sync-options'. Following sync options are supported:

- SkipDryRunOnMissingResource=true - disables dry run in resource is missing in the cluster
- Prune=false - disables resource pruning
- Validate=false - disables resource validation (equivalent to 'kubectl apply --validate=false')

How Does It Work Together?

Syncing process orders the resources in the following precedence:

- The phase
- The wave they are in (lower values first)
- By kind (e.g. namespaces first)
- By name

It then determines which the number of the next wave to apply. This is the first number where any resource is
out-of-sync or unhealthy. It applies resources in that wave. It repeats this process until all phases and waves are in
in-sync and healthy.

# Example

Find real-life example in https://github.com/argoproj/gitops-engine/blob/master/pkg/engine/engine.go
*/
package sync
