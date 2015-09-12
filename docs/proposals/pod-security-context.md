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
[here](http://releases.k8s.io/release-1.0/docs/proposals/pod-security-context.md).

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

This proposal is a dependency for other changes related to security context:

1.  [Generalized support for handling non-root use and SELinux isolation for volumes](https://github.com/kubernetes/kubernetes/pull/12944)
2.  GID and supplemental group support for pods/containers

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

## Proposed Design

### PodSecurityContext

The `PodSecurityContext` type expresses pod-level security settings and the base container-level
settings.  Pod-level security attributes such as using host network namespaces are relocated here
in the internal API:

```go
package api

type PodSpec struct {
    // other fields omitted
    SecurityContext *PodSecurityContext
}

type PodSecurityContext struct {
    // Use the host's network namespace. If this option is set, the ports that will be
    // used must be specified.
    // Optional: Default to false.
    HostNetwork bool
    // Use the host's IPC namespace
    // proposed in (https://github.com/kubernetes/kubernetes/pull/12470)
    HostIPC bool
    // The base settings for container-level security attributes
    ContainerDefaults *SecurityContext
}
```

The pod-level security attributes which are currently fields of the `PodSpec` are retained in the
V1 API:

```go
package v1

type PodSpec struct {
    // retained for backward compatibility
    HostNetwork bool
    // will be retained if this proposal goes in last
    HostIPC bool
    HostPID bool

    // other fields omitted
    SecurityContext *PodSecurityContext
}
```

The `ContainerDefaults` field specifies the base security context of all containers in the pod.
The containers' `securityContext` field is overlaid on the base security context to determine the
effective security context for the container.

For posterity and ease of reading, note the current state of `SecurityContext`:

```go
type SecurityContext struct {
    // Capabilities are the capabilities to add/drop when running the container
    Capabilities *Capabilities `json:"capabilities,omitempty"`

    // Run the container in privileged mode
    Privileged *bool `json:"privileged,omitempty"`

    // SELinuxOptions are the labels to be applied to the container
    // and volumes
    SELinuxOptions *SELinuxOptions `json:"seLinuxOptions,omitempty"`

    // RunAsUser is the UID to run the entrypoint of the container process.
    RunAsUser *int64 `json:"runAsUser,omitempty"`

    // RunAsNonRoot indicates that the container should be run as a non-root user.  If the RunAsUser
    // field is not explicitly set then the kubelet may check the image for a specified user or
    // perform defaulting to specify a user.
    RunAsNonRoot bool
}

// SELinuxOptions are the labels to be applied to the container.
type SELinuxOptions struct {
    // SELinux user label
    User string `json:"user,omitempty"`

    // SELinux role label
    Role string `json:"role,omitempty"`

    // SELinux type label
    Type string `json:"type,omitempty"`

    // SELinux level label.
    Level string `json:"level,omitempty"`
}
```

The new V1 API should be backward compatible with the existing API.  Backward compatibility is
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
2.  For fields in `pod.Spec.SecurityContext.ContainerDefaults`:
    1.  If the field **is set** in `pod.Spec.SecurityContext.ContainerDefaults`, for each container:
        1.  If the field is not set in `container.SecurityContext`, the `container.SecurityContext`
        field is set to the value of the `pod.Spec.SecurityContext.ContainerDefaults` field
    2.  If the field **is not set** in `pod.Spec.SecurityContext.ContainerDefaults`, for each
        container:
        1.  If the field is not set in `container.SecurityContext`,  no field values are altered
        2.  If the field is set in the `container.SecurityContext` of all containers and has a
            uniform value, the field in `pod.Spec.SecurityContext.ContainerDefaults` is set to be
            that value

When the V2 Pods API is created, we will drop support for the `PodSpec.HostNetwork` field and any
other host namespace control fields of the `PodSpec`.

#### Examples

Let's work through the different cases to account for with regard to backward compatibility.


1.  Pod-level security attributes

    When a new client creates a pod, if `hostNetwork` or another boolean field of the
    `pod.Spec.SecurityContext` is set **to true** in either `pod.Spec` or
    `pod.Spec.SecurityContext`, that field is set to true in the internal API.  Both of the
    following pod specs produce the same result when submitted to the API.  Consider a pod created
    by an old client:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      hostNetwork: true
      containers:
      - name: a
      - name: b
    ```

    and a pod created by a new client:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      securityContext:
        hostNetwork: true
      containers:
      - name: a
      - name: b
    ```

    both produce the same result:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      hostNetwork: true
      securityContext:
        hostNetwork: true
      containers:
      - name: a
      - name: b
    ```

2.  Pods created using `pod.Spec.SecurityContext.ContainerDefaults`:

    If a pod is created with fields of `pod.Spec.SecurityContext.ContainerDefaults` set, those
    fields will be set in the `container.SecurityContext`s of each container that does not override
    them:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      securityContext:
        containerDefaults:
          runAsUser: 1001
      containers:
      - name: a
        securityContext:
          runAsUser: 1002
      - name: b
    ```

    produces the following when submitted to the API:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      securityContext:
        containerDefaults:
          runAsUser: 1001
      containers:
      - name: a
        securityContext:
          runAsUser: 1002
      - name: b
        securityContext:
          runAsUser: 1001
    ```

3.  Synthesizing `pod.Spec.SecurityContext.ContainerDefaults` from `container.SecurityContext`:

    The fields of `pod.Spec.SecurityContext.ContainerDefault` must be synthesized from the fields
    of `container.SecurityContext` if `pod.Spec.SecurityContext.  For example, the following pod:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      containers:
      - name: a
        securityContext:
          runAsUser: 1001
      - name: b
        securityContext:
          runAsUser: 1001
    ```

    produces the following when submitted to the API:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      securityContext:
        containerDefaults:
          runAsUser: 1001
      containers:
      - name: a
        securityContext:
          runAsUser: 1001
      - name: b
        securityContext:
          runAsUser: 1001
    ```

    If all of the `container.SecurityContext` settings are disjoint, the
    `pod.Spec.SecurityContext.ContainerDefaults` should not be created.  The following pod:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      containers:
      - name: a
        securityContext:
          runAsUser: 1001
      - name: b
        securityContext:
          runAsUser: 1002
    ```

    produces the following when submitted to the API:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      containers:
      - name: a
        securityContext:
          runAsUser: 1001
      - name: b
        securityContext:
          runAsUser: 1002
    ```

4.  V1.0 object stored in etcd

    V1.0 objects that currently exist and are stored in etcd are accounted for in same way as
    new objects created by old clients.  When clients `GET` them, they go through conversion to the
    internal API and back into the versioned API.  So, the following object stored in etcd:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      hostNetwork: true
      containers:
      - name: a
        securityContext:
          selinuxOptions:
            range: 's0:c1,c2'
      - name: b
        securityContext:
          selinuxOptions:
            range: 's0:c1,c2'
    ```

    is converted to the following in API responses:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: test-pod
    spec:
      hostNetwork: true
      securityContext:
        hostNetwork: true
        containerDefaults:
          selinuxOptions:
            range: 's0:c1,c2'
      containers:
      - name: a
        securityContext:
          selinuxOptions:
            range: 's0:c1,c2'
      - name: a
        securityContext:
          selinuxOptions:
            range: 's0:c1,c2'
    ```

#### Testing

A backward compatibility test suite will be established for the v1 API.  The test suite will
verify compatibility by converting objects into the internal API and back to the version API and
examining the results.

All of the examples here will be used as test-cases.  As more test cases are added, the proposal will
be updated.

An example of a test like this can be found in the
[OpenShift API package](https://github.com/openshift/origin/blob/master/pkg/api/compatibility_test.go)

### Kubelet changes

1.  The Kubelet will use the new fields on the `PodSecurityContext` for host namespace control
2.  No changes are necessary around the new 'ContainerDefaults' settings currently.  The security
    context for a container will drive the Kubelet's behavior at runtime
3.  The container runtimes will have to change to use the fields in the new internal API location

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/proposals/pod-security-context.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
