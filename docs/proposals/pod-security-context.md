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
security settings the container uses.  In practice many of these attributes are uniform across all
containers in a pod.  Simultaneously, there is also a need to apply the security context pattern
at the pod level to correctly model security attributes that apply only at a pod level.

Users should be able to:

1.  Express security settings that are applicable to the entire pod
2.  Express base security settings that apply to all containers
3.  Override only the settings that need to be differentiated from the base in individual
    containers

Goals of this design:

1.  Describe the use cases for which a pod-level security context is necessary
2.  Describe the necessary model changes and associated implementation changes

## Constraints and assumptions

1.  We will not design for intra-pod security; we are not currently concerned about isolating
    containers in the same pod from one another
1.  We will design for backward compatibility with the current V1 API

## Use Cases

1.  As a developer, I want to correctly model security attributes which belong to an entire pod
2.  As a user, I want to be able to specify a set of container-level security attributes for
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
containers.

### Use Case: Override pod security context for container

Some use cases require the containers in a pod to run with different security settings.  As an
example, a user may want to have a pod with two containers, one of which runs as root with the
privileged setting, and one that runs as a non-root UID.  To support use cases like this, it should
be possible to override appropriate (ie, not intrinsically pod-level) security settings for
individual containers.

## Analysis


## Proposed Design

### PodSecurityContext

The `PodSecurityContext` type expresses pod-level security settings and the base container-level
settings.  

```go
type PodSpec struct {
    // retained for compatibility with existing API
    HostNetwork bool

    // other fields omitted
    SecurityContext *PodSecurityContext
}

type PodSecurityContext struct {
    // Uses the host's network namespace. If this option is set, the ports that will be
    // used must be specified.
    // Optional: Default to false.
    HostNetwork bool
    // Uses the host's IPC namespace proposed in (https://github.com/kubernetes/kubernetes/pull/12470)    HostIPC bool
    // The base settings for container-level security attributes
    ContainerDefaults *SecurityContext
}
```

The `ContainerDefaults` field specifies the base security context of all containers in the pod.
The containers' `securityContext` field is overlaid on the base security context to determine the
effective security context for the container.

### SecurityContext

A previous version of this proposal dealt with an API refactor wherein a new object was introduced
to hold the new set of container-level security attributes.  This introduced a number of complicated
backward compatibility issues; it is far simpler to retain the existing `SecurityContext` type in
our design.

For now, the `SecurityContext` type will have only additive changes made.  In the V2 API, the
`SELinuxContext` field will be removed from `SecurityContext` and will be exclusively pod level.
For now, we are required to retain this field for backward compatibility purposes; these issues are
explored in detail in the backward compatibility section.

### Backward compatibility

The existing V1 API should be backward compatible with the new API.  Backward compatibility is
defined as:

> 1.  Any API call (e.g. a structure POSTed to a REST endpoint) that worked before your change must
>     work the same after your change.
> 2.  Any API call that uses your change must not cause problems (e.g. crash or degrade behavior) when
>     issued against servers that do not include your change.
> 3.  It must be possible to round-trip your change (convert to different API versions and back) with
>     no loss of information.

Let's break down the fields of the `PodSecurityContext` and examine the backward compatibility rules
for each class of field:

1.  Fields that are purely additive have no backward compatibility concerns
2.  For fields that are moving from the `PodSpec` to the `PodSecurityContext`:
    1.  If the new field is set and the deprecated field is unset, the deprecated field is defaulted
        to the value of the new field
    2.  If the deprecated field is set and the new field is unset, the new field is defaulted to the
        value of the deprecated field
    3.  If the deprecated field and new field are both set, their values must match
2.  For container-level base settings:
    1.  If the field **is set** in the `ContainerDefaults` field, for each container:
        1.  If the field is not set in the container's `SecurityContext`, the field in the
            `SecurityContext` is set to the value of the base field
    2.  If the field **is not set** in the base field of the `PodSecurityContext`,
        for each container:
        1.  If the field is not set in the container's `SecurityContext` no field values are altered
        2.  If the field is set in the `SecurityContext` of all containers and has a uniform value,
            the base field is set to be that value

When the V2 Pods API is created, we will drop support for:

1.  The `PodSpec.HostNetwork` field and any other host namespace control fields of the `PodSpec`
2.  The `SecurityContext.SELinuxContext` field

If the container level SELinuxContext is set it will always override the pod-level SELinux context
in the V1 API.

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
    