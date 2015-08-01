## Abstract

A proposal for enabling generalized volume usage in advanced scenarios such as containers running
under non-root UIDs or with SELinux enabled.

## Motivation

Kubernetes volumes should be usable regardless of the UID a container runs as or whether SELinux
is enabled.  These scenarios cut across all volume types, so the system should be able to handle
them in a generalized way to provide uniform functionality across all volume types and lower the
barrier to new types.

Goals of this design:

1.  Enumerate the different use-cases for volumes
2.  Define the desired goal state for volumes in Kubernetes
3.  Describe a short-term approach for enabling achieveable use-cases
4.  Describe gaps that must be closed to achieve long-term goals

## Constraints and Assumptions

1.  When writing permissions in this proposal, `D` represents a don't-care value; example: `07D0`
    represents permissions where the owner has `7` permissions, all has `0` permissions, and group
    has a don't-care value
2.  Read-write usability of a volume from a container is defined as one of:
    1.  The volume is owned by the container's effective UID and has permissions `07D0`
    2.  The volume is owned by the container's effective GID or one of its supplemental groups and
        has permissions `0D70`
3.  Read-only usability of a volume from a container is defined as one of:
    1.  The volume is not owned by the container and has permissions `0DD5`
    2.  The volume is owned by the container's effective UID and has permissions `05DD`
    3.  The volume is owned the container's effective GID or one of its supplemental groups and has
        permissions `0D5D`
4.  Volume plugins should not have to handle setting permissions on volumes
5.  Volume plugins should not have to handle SELinux unless it is unavoidable during volume setup
5.  We will not support securing containers within a pod from one another

## Current State Overview

### Kubernetes

Kubernetes volumes can be divided into two broad categories:

1.  Volumes created by the kubelet on the host directory: empty directory, git repo, secret,
    downward api ([proposed](https://github.com/GoogleCloudPlatform/kubernetes/pull/5093)).  All
    volumes in this category delegate to `EmptyDir` for their underlying storage.  These volumes are
    created with ownership `root:root`.

2.  Distributed filesystems: AWS EBS, iSCSI, RBD, NFS, Glusterfs, GCE PD.  For these volumes, the
    ownership is determined by the underlying filesystem.

The `EmptyDir` volume was recently modified to create the volume directory with `0777` permissions
from `0750` to support basic usability of that volume as a non-root UID.  The `EmptyDir` volume also
has basic SELinux support, in that it creates the volume directory using the SELinux context of the
Kubelet volume directory if SELinux is enabled.

There is a [proposed change](https://github.com/GoogleCloudPlatform/kubernetes/pull/9844) to the
EmptyDir plugin that adds SELinux relabeling capabilities to that plugin, which is also carried as a
patch in [OpenShift](https://github.com/openshift/origin).

### Docker

#### UID/GID

Docker recently added supplemental group support.  This adds the ability to specify additional
groups that a container should be part of, and will be released with Docker 1.8.

There is a [proposal](https://github.com/docker/docker/pull/14632) to add a bind-mount flag to tell
Docker to change the ownership of a volume to the effective UID and GID of a container, but this has
not yet been accepted.

#### SELinux

Docker uses a base SELinux context and calculates a unique MCS label per container.  The SELinux
context of a container can be overriden with the `SecurityOpt` api that allows setting the different
parts of the SELinux context individually.

Docker has functionality to relabel bind-mounts with a usable SElinux and supports two different
use-cases:

1.  The `:Z` bind-mount flag, which tells Docker to relabel a bind-mount with the container's
    SELinux context
2.  The `:z` bind-mount flag, which tells Docker to relabel a bind-mount with the container's
    SElinux context, but remove the MCS labels, making the volume shareable beween containers

We should avoid using the `:z` flag, because it relaxes the SELinux context so that any container
(from an SELinux standpoint) can use the volume.

### Rocket

#### UID/GID

Rocket
[image manifests](https://github.com/appc/spec/blob/master/spec/aci.md#image-manifest-schema) can
specify users and groups, similarly to how a Docker image can.  A Rocket
[pod manifest](https://github.com/appc/spec/blob/master/spec/pods.md#pod-manifest-schema) can also
override the default user and group specified by the image manifest.

Rocket does not currently support supplemental groups or changing the owning UID or
group of a volume, but it has been [requested](https://github.com/coreos/rkt/issues/1309).

#### SELinux

Rocket currently reads the base SELinux context to use from `/etc/selinux/*/contexts/lxc_contexts`
and allocates a unique MCS label per pod.

## Use Cases

1.  As a user, I want the system to set ownership and permissions on volumes correctly to enabled
the following scenarios:
    1.  All containers running as root
    4.  All containers running as the same non-root user
    5.  Multiple containers running as a mix of root and non-root users
2.  As a user, I want all use-cases to work properly on systems where SELinux is enabled

### Ownership and permissions

#### All containers running as root

For volumes that only need to be used by root, no action needs to be taken to change ownership or
permissions.  For situations where read-only access to a shared volume is required from one or more
containers, the `VolumeMount`s in those containers should have the `readOnly` field set.

#### All containers running as a single non-root user

In use cases whether a volume is used by a single non-root UID the volume ownership and permissions
should be set to enable read/write access.

Currently, a non-root UID will not have permissions to write to any but an `EmptyDir` volume.
Today, users that need this case to work can:

1.  Grant the container the necessary capabilities to `chown` and `chmod` the volume:
    - `CAP_FOWNER`
    - `CAP_CHOWN`
    - `CAP_DAC_OVERRIDE`
2.  Run a wrapper script that runs `chown` and `chmod` commands to set the desired ownership and
    permissions on the volume before starting their main process

This workaround has significant drawbacks:

1.  It grants powerful kernel capabilities to the code in the image
2.  The user experience is poor; it requires changing Dockerfile, adding a layer, or modifying the
    container's command

Some users have manage the ownership of distributed file system volumes on the server side.  In this
scenario, the UID of the container using the volume is known in advance.  The ownership of the
volume is set to match the container's UID on the server side.

#### Containers running as a mix of root and non-root users

If the list of UIDs that need to use a volume includes both root and non-root users, supplemental
groups can be applied to enable sharing volumes between containers.  The ownership and permissions
`root:<supplemental group> 0770` will make a volume usable from both containers running as root and
running as a non-root UID and the supplemental group.

### Volumes and SELinux

Many users have a requirement to run pods on systems that have SELinux enabled.  Volume plugin
authors should not have to explicitly account for SELinux except for volume types that require
special handling of the SELinux context during setup.

SELinux handling for most volumes can be generalized into running a `chcon` operation on the volume
directory after running the volume plugin's `Setup` function, but there is at least one exception.
For NFS volumes, the `context` flag must be passed to `mount`, or the `virt_use_nfs` SELinux boolean
set.  If a system administrator does not wish to set `virt_use_nfs`, the correct context must be
passed to the `mount` operation in order for the volume to be usable from a container's SELinux
policy on certain systems.

## Community Design Discussion

- [kubernetes/2630](https://github.com/GoogleCloudPlatform/kubernetes/issues/2630)
- [kubernetes/11319](https://github.com/GoogleCloudPlatform/kubernetes/issues/11319)
- [kubernetes/9384](https://github.com/GoogleCloudPlatform/kubernetes/pull/9384)

## Analysis

The system needs to be able to:

1.  Model correctly which volumes require ownership / SELinux label management
1.  Determine the correct ownership and SELinux context of each volume in a pod if required
1.  Set the ownership and permissions on volumes when required
1.  Relabel volumes with the correct SELinux context when required

### What requires modeling?

In order to understand what needs to be modeled, we'll need to examine the various facets of
ownership and SELinux labeling that are required.  For now, let's establish that the types of
volumes break down into three categories:

1.  the hostPath volume
2.  volumes created by Kubernetes, which are all derived from empty dir
3.  distributed filesystems

#### The hostPath corner case:

The hostPath volume should only be used by 'trusted' users, and the permissions of paths exposed
into containers via hostPath volumes should always be managed by the cluster operator.  If the
Kubelet managed ownership or SELinux context for hostPath volumes, a user who could create a
hostPath volume could affect changes in the state of arbitrary paths within the host's filesystem.
This would be a huge security hole, so we will consider hostPath a corner case that the kubelet
should never perform ownership or label management for.

#### Volumes derived from empty dir

Empty dir and volumes derived from it are the opposite of hostPath.  Since they are created by the
system, Kubernetes must always ensure that the ownership and SELinux context (when relevant) are
set correctly for the volume to be usable.

#### Distributed file systems

Whether the ownership or SELinux label of a volume for a distributed file system should be managed
by Kubernetes depends on the cluster operator's policy and the volume type.  Additionally, not all
distributed file systems support `chown`, `chmod`, and `chcon` operations uniformly.  Our API should
make it simple to express whether a particular volume should have these concerns managed by
Kubernetes.  For simplicity's sake, we will assume that if a volume requires ownership management,
that it also requires relabeling and vice-versa.

#### API requirements

From the above, we know that ownership management must be applied:

1.  To some volume types always
2.  To some volume types never
3.  To some volume types *sometimes*

For volume types where the need for ownership/label management is not at the discretion of the user,
there should be no need (or way) to specify whether ownership management is required in the API.
Conversely there must be a way to specify this for volume types where it depends on the user's
needs.

### Determining correct ownership

There are two components of ownership management:

1.  When does Kubernetes need to manage the ownership of a volume?
2.  How does Kubernetes determine the ownership of a volume?

#### When to determine ownership

Whether Kubernetes should manage the ownership of a volume for a distributed filesystem depends upon
both the file system type and the cluster operator's policy.  For example:

1.  Some organizations will manage ownership of volumes externally to the cluster
2.  It is not possible to securely `chown` or `chmod` paths within some distributed filesystems

#### How to determine ownership

The Kubelet must analyze the pod spec to determine which UIDs need to use which volumes.  If a
container's security context's `RunAsUser` field is not set, the Kubelet must inspect the image via
the container runtime to determine which UID the image will run as.  Once the list of UIDs that need
to use a volume is known, the kubelet can determine which ownership and permissions should be used
to make the volume functional.

If a volume is used only by a single UID within the pod, the ownership can be set to that UID.
Otherwise, the volume should be owned by a group and the containers run in that group.

If a non-numeric user is specified by an image, the behavior of container runtimes is to look up the
UID from the container's `/etc/passwd` file.  It is not feasible for the kubelet to make this
determination; we may not be able to correctly support non-numeric users in image metadata.  If a
cluster operator allows users to create pods with images that specify non-numeric users, the system
must use supplemental groups to make volumes shareable.

### Setting ownership and permissions on volumes

For volumes that are created by the system, it is sufficient to perform `chown` and `chmod`
operations on the host's filesystem.  For some distributed file system volumes, such as ones based
on remote block devices, `chown` and `chmod` applied locally will also work correctly.  File systems
based on RPC, however, may not support client-side `chown` and `chmod`.  The system would need to
schedule the `chown` and `chmod` operations to happen on the server side for these volumes.

#### A note on `chown`, `chmod`, and distributed filesystems

Distributed file systems based on remote block devices should support `chown` and `chmod` operations
correctly.

For distributed file systems based on RPC, the success of `chown` and `chmod` operations on
distributed filesystems can depend on the configuration of the server hosting the volumes.  It may
not be appropriate to perform `chown` or `chmod` operations on some distributed file systems.
For example, actions taken by root on an NFS client are treated as `nobody` on the server unless the
`no_squash_root` setting is enabled for that volume's NFS export.  Enabling `no_squash_root`
contradicts Red Hat's security guidance for NFS (as an example), so there will need to be another
mechanism that prepares some distributed fs volumes.

One possibility is that if the server hosting the filesystem is itself a Kubernetes node, a pod
could be scheduled onto that node that prepared the volume by setting the ownership, permissions,
and SELinux context.

### Relabeling volumes with correct SELinux context

On systems with SELinux enabled, volumes should be relabeled with the correct SELinux context.
Docker has this capability today; it is desireable for other container runtime implementations to
provide similar functionality.

Relabeling should be an optional aspect of a volume plugin to accomodate:

1.  volume types for which generalized relabeling support is not sufficient
2.  testing for each volume plugin individually

#### SELinux and distributed file systems

Some distributed filesystems have complications where SELinux is involved.  Let's look at NFS as an
example.  Files in NFS volumes cannot have their contexts changed with `chcon` after the volume is
mounted; the `context` argument must be passed to the `mount` call.

For NFS, there is an SELinux boolean, `virt_use_nfs` that allows containers to use files with the
SELinux type `nfs_t`.  Setting `virt_use_nfs` on a system would allow containers to use *any* NFS
mount without the kubelet having to relabel the files.  However, the semantics would be different
from what might be expected, since SELinux will not provide any protection against containers from
one pod using files in an NFS mount belonging to another pod.

NFS is a case where the `chcon` approach isn't going to solve the whole problem.  Instead, the NFS
volume plugin will have to handle calling `mount` with the `context` argument in order to gain
cross-container protection from SELinux.

Not all distributed file systems support SELinux uniformly.  Some file systems provide only the
coarse-level control via an SELinux boolean to determine whether containers can use them.  The
internal API should account for this.

## Proposed Design

Our proposed design should minimize code for handling ownership and SELinux required in:

1.  volume plugins
2.  the kubelet

### API changes

There are a couple factors to consider for the API:

1.  Currently, it is possible to specify a different SELinux context for each container in a pod
2.  There must be a way to specify a supplemental group that all containers in a pod are part of
3.  There should be a uniform way to specify in the API that a volume requires ownership / label
    management

As stated in the assumptions of this proposal, we will not secure containers in a pod from one
another.  This greatly simplifies the story for SELinux -- we can make the SELinux context uniform
across the containers in a pod and remove it from the container level entirely.  The pod-level
security proposal deals with this in detail.

The pod-level supplemental group is necessary to capture groups that cut across all containers in a
pod, which is the simplest way to guarantee that all arbitrary combinations of UID/GIDs in
containers can share a volume.  The pod-level security context proposal also deals with this change
in detail.

Finally, we must have a clear way for users to indicate that Kubernetes should manage ownership and
labeling of a volume, when it is possible.  For this, the `VolumeSource` should have a new field,
`Manage`, and the volume `Builder` interface have two new methods, `RequiresOwnershipManagement()`
and `RequiresLabelManagement()`.

The `Builder` implementation for each type will be responsible for correctly indicating whether a
particular volume requires ownership and label management:

1.  The hostPath builder should always return `false` for both management types
2.  The empty dir builder and builders for plugins derived from it should always return `true`
3.  The builders for distributed file systems should return the correct values based on the `Manage`
    field of the volume source and the SELinux support of that volume type.

TODO: persistent volumes

### Kubelet changes

The Kubelet should be modified to perform ownership and label management when required for a volume.
The `mountExternalVolumes` method should call `RequiresOwnershipManagement()` and
`RequiresLabelManagement()` on the builder for each volume to determine whether a local `chcon`,
`chmod`, or `chcon` operation is required.

Container runtime implementations should receive information about whether relabeling for volumes
should be performed.  The docker runtime implementation should be modified to support relabeling.

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/proposals/volumes.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
    