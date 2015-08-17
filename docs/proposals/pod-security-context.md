<!-- BEGIN MUNGE: UNVERSIONED_WARNING -->

<!-- BEGIN STRIP_FOR_RELEASE -->

<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">

<h2>PLEASE NOTE: This document applies to the HEAD of the source tree</h2>

If you are using a released version of Kubernetes, you should
refer to the docs that go with that version.

<strong>
The latest 1.0.x release of this document can be found
[here](http://releases.k8s.io/release-1.0/docs/proposals/apiserver_watch.md).

Documentation for other releases can be found at
[releases.k8s.io](http://releases.k8s.io).
</strong>
--

<!-- END STRIP_FOR_RELEASE -->

<!-- END MUNGE: UNVERSIONED_WARNING -->

## Abstract

A proposal for refactoring `SecurityContext` to have pod-level and container-level facets in order
to correctly model pod- and container-level security concerns.

## Motivation

Currently, containers have a `SecurityContext` attribute which contains information about the
security settings the container uses.  A container-level context lacks the ability, however, to
express notions that are really pod-level.  A pod-level security context will:

1.  Allow users to cleanly express security settings that are applicable to the entire pod
2.  Allow users to express default settings for the container-level context and override only the
    settings that need to be differentiated from the pod security context in containers where
    required

Goals of this design:

1.  Describe the use-cases for which a pod-level security context is necessary
2.  Describe the model changes and associated implementation changes for the refactor

## Use Cases

1.  As a user, I want to be able to specify a default security context for all containers in a pod
2.  As a user, I want to be able to override applicable pieces of the pod security context for
    individual containers as needed

### Use Case: Default security context for all containers

By default, all containers in a pod should share the same security settings, and those settings
should only have to be specified once.  If users are forced to specify the same settings for each
container, it requires more complex validations and allows for transcription errors between
containers

Additionally, use-cases for sharing resources such as volumes are much easier to implement if a user
can describe a pod-level security context.

### Use Case: Override pod security context for container

Some use-cases require the containers in a pod to run with different security settings.  As an
example, a user may want to have a pod with two containers, one of which runs as root with the
privileged setting, and one that runs as a non-root UID.  To support use-cases like this, it should
be possible to override appropriate (ie, not intrinsically pod-level) security settings for
individual containers.

## Proposed Design

The `SecurityContext` type should be replaced by two new, similar types: `ContainerSecurityContext`
and `PodSecurityContext`:

### ContainerSecurityContext

The `ContainerSecurityContext` type models the security settings that specified for a container:

```go
type ContainerSecurityContext struct {
    Capabilities              *Capabilities
    Privileged                *bool
    SELinuxOptions            *SELinuxOptions
    RunAsUser                 *int64
    RunAsNonRoot              bool
    RunAsGroup                *int64
    RunWithSupplementalGroups []int64
}
```

The `ContainerSecurityContext` type is very similar to the existing `SecurityContext` type, with
two additions:

1.  The `RunAsGroup` field specifies the GID the container process runs as
2.  The `RunWithSupplementalGroups` field specifies additional groups the container process should
    be in

The addition of these fields enables scenarios where containers share resources via groups.

### PodSecurityContext

The `PodSecurityContext` type expresses pod-level security settings and the default container-level
settings:

```go
type PodSecurityContext struct {
    HostNetwork       bool
    HostIpc           bool
    ContainerDefaults *ContainerSecurityContext
}
```

The `PodSecurityContext` type should be extended to encompass future pod-level security settings.

The `PodSpec` type should be modified to use `PodSecurityContext` instead of the `HostNetwork` and
(proposed) `HostIpc` settings:

```go
type PodSpec struct {
    // other fields omitted
    SecurityContext *PodSecurityContext
}
```

### Kubelet changes

#### Effective security context

The Kubelet should be modified to determine the effective run-time security context for each
container by projecting the overrides for that container onto the `ContainerDefaults` field of the
pod security context.

#### Container runtime changes

The docker and rkt `Runtime` implementations should be changed to set the group of each container
process using the `RunAsGroup` field of the container's effective security context.

The docker runtime should be modified to set the supplemental groups of a container's process based
on the `RunWithSupplementalGroups` field of the container's effective security context.  Rocket
does not currently support supplemental groups.

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/proposals/pod-security-context.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
    