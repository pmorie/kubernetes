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

Documentation for other releases can be found at
[releases.k8s.io](http://releases.k8s.io).
</strong>
--

<!-- END STRIP_FOR_RELEASE -->

<!-- END MUNGE: UNVERSIONED_WARNING -->

## Abstract

Real Kubernetes clusters have a variety of volumes which differ widely in
size, iops performance, retention policy, and other characteristics.  A
mechanism is needed to enable administrators to describe the taxonomy of these
volumes, and for users to make claims on these volumes based on their
attributes within this taxonomy.

A label-selector mechanism is proposed to enable flexible selection of volumes
by persistent volume claims.  An API extension called "storage classes" is
also proposed which initially allows administrators to formalize descriptions
of the taxonomy and which will form the basis of dynamic provisioning of
storage volumes.

## Motivation

Currently, users of persistent volumes have the ability to make claims on
those volumes based on some criteria such as the access modes the volume
supports and minimum resources offered by a volume.  In an organization, there
are often more complex requirements for the storage volumes needed by
different groups of users.  A mechanism is needed to model these different
types of volumes and to allow users to select those different types without
being intimately familiar with their underlying characteristics.

As an example, many cloud providers offer a range of performance
characteristics for storage, with higher performing storage being more
expensive.  Cluster administrators want the ability to:

1.  Invent a taxonomy of storage classes that correspond to different sets of
    volume types and performance characteristics
2.  Allow users to make claims on volumes of a certain class without knowing
    understanding its technical properties

## Constraints and Assumptions

The proposed design should:

1.  Deal with manually-created volumes
2.  Not require users to understand the differences between storage classes
3.  Allow administrators to describe their own taxonomy of classes
4.  Eventually allow advanced dynamic provisioning features to be created
    based on storage classes, eg: configure the provisioner for the class by
    adding provisioner parameters to the class itself

We will focus **only** on the barest mechanisms to describe and implement
storage classes in this proposal.  We will address the following topics in
future proposals:

1.  Dynamically provisioning new volumes for a storage class
2.  Allowing administrators to control access to storage classes

## Use Cases

1.  As an administrator, I want to create a taxonomy of storage classes which
    models the different combinations of (volume type, performance
    characteristics) of the persistent volumes available in my cluster
2.  As a user, I want to be able to make a claim on a persistent volume by
    specifying the storage class as well as the currently available attributes

### Use Case: Taxonomy of Storage Classes

Kubernetes offers volume types for a variety of storage systems.  Within each
of those storage systems, there are numerous ways in which volume instances
may differ from one another: iops performance, retention policy, etc.
Administrators of real clusters typically need to manage a variety of
different volumes whose characteristics correspond to some logical class of
their own devising.

Kubernetes should make it possible for administrators to flexibly model the
taxonomy of volumes in their clusters and to label volumes with their storage
class.  This capability must be optional and fully backward-compatible with
the existing API.

As an example, there are four different types of AWS EBS volume (in ascending
order of performance):

1.  Cold HDD
2.  Throughput optimized HDD
3.  General purpose SSD
4.  Provisioned IOPS SSD

Currently, there is no way to distinguish between a group of 4 PVs where each
volume is of one of these different types.  Administrators need the ability to
distinguish between these instances and to give their own names to the
different types.  An administrator might decide to name the classes of the
volumes as follows:

1.  Cold HDD - `tin`
2.  Throughput optimized HDD - `bronze`
3.  General purpose SSD - `silver`
4.  Provisioned IOPS SSD - `gold`

This is not the only dimension that EBS volumes can differ in.  Let's simplify
things and imagine that AWS has two availability zones, `east` and `west`. Our
administrators want to differentiate between volumes of the same type in these
two zones, so they create a taxonomy of volumes like so:

1.  `tin-west`
2.  `tin-east`
3.  `bronze-west`
4.  `bronze-east`
5.  `silver-west`
6.  `silver-east`
7.  `gold-west`
8.  `gold-east`

Another administrator of the same cluster might label things differently,
choosing to focus on the business role of volumes.  Say that the data
warehouse department is the sole consumer of the cold HDD type, and the DB as
a service offering is the sole consumer of provisioned IOPS volumes.  The
administrator might decide on the following taxonomy of volumes:

1.  `warehouse-east`
2.  `warehouse-west`
3.  `dbaas-east`
4.  `dbaas-west`

There are any number of ways an administrator may choose to distinguish
between volumes.  Labels are used in Kubernetes to express the user-defined
properties of API objects and are a good fit to express this information for
volumes.  In the examples above, administrators might differentiate between
the classes of volumes using the labels `business-unit`, `volume-type`, or
`region`.

Administrators should also be able to give each storage class a name and a
user-facing description.  In the future, storage classes may also hold
configuration data for provisioners that create volumes.

### Use Case: Claims by Storage Class

Users have different levels of knowledge about storage systems, so it must be
possible to make a claim on a volume belonging to a storage class without
being familiar with or even conceptually understanding the characteristics of
that class.

A user must be able to view which storage classes are available.  Storage
classes must be addressable by name to allow users to reference them simply.

If a user specifies a storage class with a claim and one or more of the
existing criteria (such as capacity), then a volume must satisfy all of those
conditions to be bound to the claim.

Label selectors are used through the Kubernetes API to describe relationships
between API objects using flexible, user-defined criteria.  It makes sense to
use the same mechanism with persistent volumes and storage claims to provide
the same functionality for these API objects.

## Proposed Design

We propose that:

1.  A new field called `persistentVolumeSelector` be added to the
    `PersistentVolumeClaimSpec` type
2.  The persistent volume controller be modified to account for the selector
    when selecting volumes to bind a claim to.
3.  A new API extension resource be added called `StorageClass`

### Persistent Volume Selector

Label selectors are used throughout the API to allow users to express
relationships in a flexible manner.  The problem of selecting a volume to match
a claim fits perfectly within this metaphor.  Adding a label selector to
`PersistentVolumeClaimSpec` will allow users to:

1.  Express the desired storage class as a label selector
2.  Use arbitrary labels and selectors to drive PV binding claims

```go
// PersistentVolumeClaimSpec describes the common attributes of storage devices
// and allows a Source for provider-specific attributes
type PersistentVolumeClaimSpec struct {
    // Contains the types of access modes required
    AccessModes []PersistentVolumeAccessMode `json:"accessModes,omitempty"`
    // PersistentVolumeSelector is a selector which must be true for the claim to bind to a volume
    PersistentVolumeSelector *unversioned.LabelSelector `json:"persistentVolumeSelector,omitempty"`
    // Resources represents the minimum resources required
    Resources ResourceRequirements `json:"resources,omitempty"`
    // VolumeName is the binding reference to the PersistentVolume backing this claim
    VolumeName string `json:"volumeName,omitempty"`
}
```

### New `StorageClass` resource

A new API extension resource should be added called `StorageClass`:

```go
// Storage class is an admin-defined
type StorageClass struct {
    unversioned.TypeMeta `json:",inline"`
    ObjectMeta           `json:"metadata,omitempty"`

    Description string `json:"description,omitempty"`
}
```

It should be noted here that `StorageClass` in this proposal is basically
meant only to allow administrators to record a model of their taxonomy of
storage classes within Kubernetes.  Future features such as dynamic
provisioning will extend this type with additional fields.

The usual implementation tasks will exist for this new type:

1.  Add to extensions API
2.  Add REST storage implementation
3.  Add `kubectl` bits like describe

The labels of the `StorageClass` describe how a volume belonging to that
storage class should be labeled.

For example, say that an administrator defines a `StorageClass` called `my-
storage-class` described by the labels `type=bar` and `color=foo`:

```yaml
apiVersion: extensions/v1
kind: StorageClass
metadata:
  name: my-storage-class
  labels:
    type: bar
    color: foo
description: A very special storage class
```

#### New API resource or ConfigMap?

During discussion of this topic in
[kubernetes/17056](https://github.com/kubernetes/kubernetes/pull/17056), it
was assumed that we would use `ConfigMap` to hold information about storage
classes.  This would not be impossible to do, but there are several challenges
due to the fact that `PersistentVolume` is a global resource and `ConfigMap`
is namespaced.   We would need a virtual resource which appeared to be global
but which was a view on a namespaced resource -- no such pattern is currently
implemented anywhere in the system.  For that virtual resource, we would need
to solve all the permission/policy issues in Kubernetes and downstream
projects like OpenShift.  We would also likely need to define reserved key
names for certain attributes of `StorageClass` within the `ConfigMap` `data`
field.

Even if this approach was considered to be a good design, in order for it to
be worth it, the time required to implement it would have to be less than the
time required for a new global resource.  Since this approach is full of
unknowns, and global resources are well-understood, it makes sense to propose
that we go the route of the new global resource.

### Labeling volumes with storage class

To indicate that a volume belongs to a storage class, it should be labeled
with the same labels as the `StorageClass` it is part of.  The following
volume is part of the `my-storage-class` class:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: ebs-pv-1
  labels:
    type: bar
    color: foo
spec:
  capacity:
    storage: 100Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  awsElasticBlockStore:
    volumeID: vol-12345
    fsType: xfs
```

### Controller Changes

At the time of this writing, the various controllers for persistent volumes
are in the process of being refactored into a single controller (see
[kubernetes/24331](https://github.com/kubernetes/kubernetes/pull/24331)).

The resulting controller should be modified to use the new
`persistentVolumeSelector` field to match a claim to a volume.  In order to
match to a volume, all criteria must be satisfied; ie, if a label selector is
specified on a claim, a volume must match both the label selector and any
specified access modes and resource requirements to be considered a match.

## Examples

Let's take a look at a few examples, revisiting the taxonomy of EBS volumes and regions:

Say that the administrator of a cluster decides to use the `storage-class` and
`region` labels to model their taxonomy of classes.  The `gold-` classes would look like:

```yaml
apiVersion: extensions/v1
kind: StorageClass
metadata:
  name: gold-west
  labels:
    storage-class: gold
    region: west
description: Provisioned IOPS SSD in the west region

apiVersion: extensions/v1
kind: StorageClass
metadata:
  name: gold-east
  labels:
    storage-class: gold
    region: east
description: Provisioned IOPS SSD in the east region
```

...and volumes in these classes would look like:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: ebs-pv-west
  labels:
    storage-class: gold
    region: west
spec:
  capacity:
    storage: 150Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  awsElasticBlockStore:
    volumeID: vol-23456
    fsType: xfs

apiVersion: v1
kind: PersistentVolume
metadata:
  name: ebs-pv-east
  labels:
    storage-class: gold
    region: east
spec:
  capacity:
    storage: 150Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  awsElasticBlockStore:
    volumeID: vol-34567
    fsType: xfs
```

...and claims on these volumes would look like:

```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: ebs-claim-west
spec:
  accessModes:
    - ReadWriteMany 
  resources:
    requests:
      storage: 1Gi
  persistentVolumeSelector:
    matchLabels:
      storage-class: gold
      region: west

kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: ebs-claim-east
spec:
  accessModes:
    - ReadWriteMany 
  resources:
    requests:
      storage: 1Gi
  persistentVolumeSelector:
    matchLabels:
      storage-class: gold
      region: east
```

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/design/storage_classes.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
