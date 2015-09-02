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

A proposal for refactoring `SecurityContext` to have pod-level and container-level attributes in
order to correctly model pod- and container-level security concerns.

## Motivation

Currently, containers have a `SecurityContext` attribute which contains information about the
security settings the container uses.  A container-level context lacks the ability, however, to
express notions that are really pod-level:

1.  Whether the pod should use a host kernel namespace such as the host network, IPC, or PID
    namespace
2.  Pod-level attributes that apply to all containers
3.  Default container-level attributes that containers can override

A pod-level security context will:

1.  Allow users to cleanly express security settings that are applicable to the entire pod
2.  Allow users to express defaults for container-level settings and override only the settings that
    need to be differentiated from the defaults in individual containers

Goals of this design:

1.  Describe the use cases for which a pod-level security context is necessary
2.  Describe the model changes and associated implementation changes for the refactor

## Constraints and assumptions

1.  We will not design for intra-pod security; we are not currently concerned about isolating
    containers in the same pod from one another
1.  We will design for backward compatibility with the current V1 API

## Use Cases

1.  As a developer, I want to correctly model security attributes which belong to an entire pod
2.  As a user, I want to be able to specify a default set of container-level security attributes for
    all containers in a pod
3.  As a user, I want to be able to override certain container-level security attributes for
    individual containers as needed

### Use Case: Pod level security attributes

Some security attributes make sense only to model at the pod level.  For example, it is a
fundamental property of pods that all containers in a pod share the same network namespace.
Therefore, using the host namespace makes sense to model at the pod level only, and indeed, today
it is part of the `PodSpec`.  Other host namespace support is currently being added and these will
also be pod-level settings; it makes sense to model them as a pod-level collection of security
attributes.

### Use Case: Default security context for all containers

By default, all containers in a pod should share the same security settings, and those settings
should only have to be specified once.  If users are forced to specify the same settings for each
container, it requires more complex validations and allows for transcription errors between
containers

Additionally, use cases for sharing resources such as volumes are much easier to implement if a user
can describe a pod-level security context.

### Use Case: Override pod security context for container

Some use cases require the containers in a pod to run with different security settings.  As an
example, a user may want to have a pod with two containers, one of which runs as root with the
privileged setting, and one that runs as a non-root UID.  To support use cases like this, it should
be possible to override appropriate (ie, not intrinsically pod-level) security settings for
individual containers.

## Analysis

### SELinux context: pod- or container- level?

Currently, SELinux context is specifiable only at the container level.  This is an inconvenient
factoring for sharing volumes and other SELinux-secured resources between containers because there
is no way in SELinux to share resources between processes with different MCS labels except to
remove MCS labels from the shared resource.  This is a big security risk: _any container_ in the
system can work with a resource which has the same SELinux context as it and no MCS labels.  Since
we are also not interested in isolating containers in a pod from one another, the SELinux context
should be shared by all containers in a pod to facilitate isolation from the containers in other
pods and sharing resources amongst all the containers of a pod.

### Pod-level supplemental groups

The [generalized non-root and SELinux support for volumes](https://github.com/kubernetes/kubernetes/pull/12944)
pull request describes in detail the cases for sharing volumes using supplemental groups.  The
easiest way to share resources among containers running as different UID/primary GID combinations is
to make them owned by a supplemental group that all containers run as.  Therefore, there should be a
pod-level supplemental group field that cannot be overridden in containers.

### Backward compatibility

The existing V1 API should be backward compatible with the new API.  Backward compatibility is
defined as:

1.  Any API call (e.g. a structure POSTed to a REST endpoint) that worked before your change must
    work the same after your change.
2.  Any API call that uses your change must not cause problems (e.g. crash or degrade behavior) when
    issued against servers that do not include your change.
3.  It must be possible to round-trip your change (convert to different API versions and back) with
    no loss of information.

This essentially means that we must keep all the existing fields in place in the V1 API and make
only additive changes.  So, in order to design the correct API changes, we must:

1.  Retain the current container level `SecurityContext`
2.  Define which fields must be controllable at the container level; this becomes the new 
    `ContainerSecurityContext`
3.  Define which fields must be controllable at the pod level; this becomes the new
    `PodSecurityContext`; fields in the `PodSecurityContext` are one of the following:
    1.  New fields (SupplementalGroupId)
    2.  Fields that are currently part of the `PodSpec`
    3.  Fields that are currently part of the container level `SecurityContext` and are not part
        of the `ContainerSecurityContext`
    4.  Fields that are currently part of the container level `SecurityContext` and **are** in the
        `ContainerSecurityContext` -- these will be fields of the `ContainerDefaults`

Let's break down the fields of the `PodSecurityContext` and examine the backward compatibility rules
for each class of field:

1.  Fields that are purely additive have no backward compatibility concerns
2.  For fields that are moving from the `PodSpec` to the `PodSecurityContext`:
    1.  If the new field is set and the deprecated field is unset, the deprecated field is defaulted
        to the value of the new field
    2.  If the deprecated field is set and the new field is unset, the new field is defaulted to the
        value of the deprecated field
    3.  If the deprecated field and new field are both set, their values must match
3.  For fields that are currently part of the `SecurityContext` **and are not** in the
    `ContainerSecurityContext`:
    1.  If the new field is set and the deprecated field is unset, the deprecated field is defaulted
        to the value of the new field in all containers in the pod
    2.  If the deprecated field is set and has the **same value** in each container and the new
        field is unset, the new field is set to the value of the deprecated fields
    3.  If the deprecated field is set and has more than a single value across all containers in a
        pod, the new field remains unset and the deprecated field takes precedent
4.  For fields that are currently in the `SecurityContext` **and are** in the
    `ContainerSecurityContext`:
    1.  If the field **is set** in the `ContainerDefaults` field of the `PodSecurityContext`, for
        each container the following conditions apply:
        1.  If the field is not set in the container's `SecurityContext` or
            `ContainerSecurityContext` of the container, the deprecated field of the
            `SecurityContext` is set to the value of the field in the container defaults of the
            `PodSecurityContext`
        2.  If the field is set in the `SecurityContext` and **not set** in the container's
            `ContainerSecurityContext`, the field of the `ContainerSecurityContext` is set to the
            value of the field in the container defaults of the `PodSecurityContext`
        3.  If the field is set in the `ContainerSecurityContext` and **not set** in the
            `SecurityContext`, the field of the `SecurityContext` is set to the value of the field
            in the container defaults of the `PodSecurityContext`
        4.  If the field is set in both the `SecurityContext` and `ContainerSecurityContext`, the
            values of the fields must match
    2.  If the field **is not set** in the `ContainerDefaults` field of the `PodSecurityContext`,
        for each container the following conditions apply:
        1.  If the field is not set in the container's `SecurityContext` or
            `ContainerSecurityContext` of the container, no field values are altered
        2.  If the field is set in the `SecurityContext` and **not set** in the container's
            `ContainerSecurityContext`, the field of the `ContainerSecurityContext` is set to the
            value of the field in the `SecurityContext`
        3.  If the field is set in the `ContainerSecurityContext` and **not set** in the
            `SecurityContext`, the field of the `SecurityContext` is set to the value of the field
            in the `ContainerSecurityContext`
        4.  If the field is set in both the `SecurityContext` and `ContainerSecurityContext`, the
            values of the fields must match

When the V2 pods API is created, we will drop support for the existing container-level
`SecurityContext`.

## Proposed Design

The `SecurityContext` type should be replaced by two new, similar types: `PodSecurityContext` and
`ContainerSecurityContext`.

### PodSecurityContext

The `PodSecurityContext` type expresses pod-level security settings and the default container-level
settings:

```go
type PodSecurityContext struct {
    // Uses the host's network namespace. If this option is set, the ports that will be
    // used must be specified.
    // Optional: Default to false.
    HostNetwork bool

    // Uses the host's IPC namespace proposed in (https://github.com/kubernetes/kubernetes/pull/12470)
    HostIPC bool

    // The supplemental group ID that will own volumes in this pod and that all containers will run
    // under as a supplemental group
    SupplementalGroupID *int64

    // The pod-level SELinux context
    SELinuxContext *SELinuxOptions

    // The default settings for container-level security attributes
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

### ContainerSecurityContext

The `ContainerSecurityContext` type models the security settings that specified for a container:

```go
type ContainerSecurityContext struct {
    // The kernel capabilities the container runs with
    Capabilities *Capabilities

    // Whether this is a privileged container
    Privileged *bool

    // The primary UID of the container
    UserID *int64

    // The primary GID of the container
    GroupID *int64

    // Additional supplemental groups the container should run as
    // (does not override pod-level supplemental group)
    SupplementalGroupIDs []int64

    // Validate that container shouldn't run as UID 0 if
    // we should delegate to the image's default UID
    RunAsNonRoot bool
}
```

The `ContainerSecurityContext` type is very similar to the existing `SecurityContext` type; the
following changes are made:

1.  The `GroupID` field specifies the primary GID the container process runs as
2.  The `SupplementalGroupIDs` field specifies additional groups the container process should
    be in (in addition to the pod-level supplemental group)
3.  SELinux context can no longer be set at the container level



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
    