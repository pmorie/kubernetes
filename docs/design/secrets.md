# Secret Distribution

## Abstract

A proposal for the distribution of secrets (passwords, keys, etc) to containers inside Kubernetes
using a custom volume type.

## Motivation

Secrets are needed in containers to access internal resources like the Kubernetes master or
external resources such as git repositories, databases, etc. 

A goal of this design is to eliminate or minimize the modifications to containers in order to
access secrets. Secrets should be placed where the container expects them to be.

## Constraints and Assumptions

*  This design does not prescribe a method for storing secrets; storage of secrets should be
   pluggable to accomodate different use-cases
*  Encryption of secret data and node security are orthogonal concerns
*  It is assumed that node and master are secure and that compromising their security could also
   compromise secrets:
   *  If a node is compromised, only the secrets for the containers scheduled on it should be
      exposed
   *  If the master is compromised, all secrets in the cluster may be exposed

## Use Cases

1.  As a user, I want to store secret artifacts for my applications and consume them securely in
    containers, so that I can keep the configuration for my applications separate from the images
    that use them:
    1.  As a cluster operator, I want to allow a pod to access the Kubernetes master using a custom
        `.kubeconfig` file, so that I can securely reach the master
    2.  As a cluster operator, I want to allow a pod to access a Docker registry using credentials
        from a `.dockercfg` file, so that containers can push images
    3.  As a cluster operator, I want to allow a pod to access a git repository using SSH keys,
        so that I can push and fetch to and from the repository
2.  As a user, I want to allow containers to consume supplemental information about services such
    as username and password which should be kept secret, so that I can share secrets about a
    service amongst the containers in my application securely
3.  As a user, I want to associate a pod with a `ServiceAccount` that consumes a secret and have
    the kubelet implement some reserved behaviors based on the types of secrets the service account
    consumes:
    1.  Use credentials for a docker registry to pull the pod's docker image
    2.  Present kubernetes auth token to the pod or transparently decorate traffic between the pod
        and master service

### Use-Case: Configuration artifacts

Many configuration files contain secrets intermixed with other configuration information.  For
example, a user's application may contain a properties file than contains database credentials,
SaaS API tokens, etc.  Users should be able to consume configuration artifacts in their containers
and be able to control the path on the container's filesystems where the artifact will be
presented.

### Use-Case: Metadata about services

Most pieces of information about how to use a service are secrets.  For example, a service that
provides a MySQL database needs to provide the username, password, and database name to consumers
so that they can authenticate and use the correct database. Containers in pods consuming the MySQL
service would also consume the secrets associated with the MySQL service.

### Use-Case: Secrets associated with service accounts

[Service Accounts](https://github.com/GoogleCloudPlatform/kubernetes/pull/2297) are proposed as a
mechanism to decouple capabilities and security contexts from individual human users.  A
`ServiceAccount` contains references to some number of secrets.  A `Pod` can specify that it is
associated with a `ServiceAccount`.  When a pod is associated with a service account the Kubelet
may take action based on the type of secrets the service account consumes.

#### Example: service account consumes auth token secret

As an example, the service account proposal discusses service accounts consuming secrets which
contain kubernetes auth tokens.  When a Kubelet starts a pod associates with a service account
which consumes this type of secret, the Kubelet may take a number of actions:

1.  Expose the secret in a `.kubernetes_auth` file in a well-known location in the container's
    file system
2.  Configure that node's `kube-proxy` to decorate HTTP requests from that pod to the 
    `kubernetes-master` service with the auth token, e. g. by adding a header to the request
    (see the [LOAS Daemon](https://github.com/GoogleCloudPlatform/kubernetes/issues/2209) proposal)

#### Example: service account consumes docker registry credentials

Another example use case is where a pod is associated with a secret containing docker registry
credentials.  The Kubelet could use these credentials for the docker pull to retrieve the image.

## Deferral: Consuming secrets as environment variables

Some images will expect to receive configuration items as environment variables instead of files.
We should consider what the best way to allow this is; there are a few different options:

1.  Force the user to adapt files into environment variables.  Users can store secrets that need to
    be presented as environment variables in a format that is easy to consume from a shell:

        $ cat /etc/secrets/my-secret.txt
        export MY_SECRET_ENV=MY_SECRET_VALUE

    The user could `source` the file at `/etc/secrets/my-secret` prior to executing the command for
    the image either inline in the command or in an init script, 

2.  Give secrets an attribute that allows users to express the intent that the platform should
    generate the above syntax in the file used to present a secret.  The user could consume these
    files in the same manner as the above option.

3.  Give secrets attributes that allow the user to express that the secret should be presented to
    the container as an environment variable.  The container's environment would contain the
    desired values and the software in the container could use them without accomodation the
    command or setup script.

For our initial work, we will treat all secrets as files to narrow the problem space.  There will
be a future proposal that handles exposing secrets as environment variables.

## Flow analysis of secret data with respect to the API server

There are two fundamentally different use-cases for access to secrets:

1.  CRUD operations on secrets by their owners
2.  Read-only access to the secrets needed for a particular node by the kubelet

### Use-Case: CRUD operations by owners

In use cases for CRUD operations, the user experience for secrets should be no different than for
other API resources.

TODO: Document relation with:

1.  [Service Accounts](https://github.com/GoogleCloudPlatform/kubernetes/pull/2297)
2.  [Security Contexts](https://github.com/GoogleCloudPlatform/kubernetes/pull/3910)

#### Data store backing the REST API

The data store backing the REST API should be pluggable because different cluster operators will
have different preferences for the central store of secret data.  Some possibilities for storage:

1.  An etcd collection alongside the storage for other API resources
2.  A collocated [HSM](http://en.wikipedia.org/wiki/Hardware_security_module)
3.  An external datastore such as an external etcd, RDBMS, etc.

#### Size limit for secrets

There should be a size limit for secrets in order to:

1.  Prevent DOS attacks against the API server
2.  Allow kubelet implementations that prevent secret data from touching the node's filesystem

The size limit should satisfy the following conditions:

1.  Large enough to store common artifact types (encryption keypairs, certificates, small
    configuration files)
2.  Small enough to avoid large impact on node resource consumption (storage, RAM for tmpfs, etc)

To begin discussion, we propose an initial value for this size limit of **1MB**.

### Use-Case: Kubelet read of secrets for node

The use-case where the kubelet reads secrets has several additional requirements:

1.  Kubelets should only be able to receive secret data which is required by pods scheduled onto
    the kubelet's node
2.  Kubelets should have read-only access to secret data
3.  Secret data should not be transmitted over the wire insecurely
4.  Kubelets must ensure pods do not have access to each other's secrets

#### Read of secret data by the Kubelet

The Kubelet should only be allowed to read secrets which are consumed by pods scheduled onto that
Kubelet's node and their associated service accounts.  Authorization of the Kubelet to read this
data would be delegated to an authorization plugin and associated policy rule.

#### Secret data on the node

Consideration must be given to whether secret data should be allowed to be at rest on the node:

1.  If secret data is not allowed to be at rest, the size of secret data becomes another draw on the
    node's RAM - should it affect scheduling?
2.  If secret data is allowed to be a rest, should it be encrypted?
    1.  If so, how should be this be done?
    2.  If not, what threats exist?  What types of secret are appropriate to store this way?

TODO: expand

## Community work:

Several proposals / upstream patches are notable as background for this proposal:

1.  [Docker vault proposal](https://github.com/docker/docker/issues/10310)
2.  [Specification for image/container standardization based on volumes](https://github.com/docker/docker/issues/9277)
3.  [Kubernetes service account proposal](https://github.com/GoogleCloudPlatform/kubernetes/pull/2297)
4.  [Secret proposal for docker](https://github.com/docker/docker/pull/6075)
5.  [Continuating of secret proposal for docker](https://github.com/docker/docker/pull/6697)

## Proposed Design

We propose a new `Secret` resource which is mounted into containers with a new volume type. Secret
volumes will be handled by a volume plugin that does the actual work of fetching the secret and
storing it. Secrets contain multiple pieces of data that are presented as different files within
the secret volume (example: SSH key pair).

In order to remove the burden from the end user in specifying every file that a secret consists of,
it should be possible to mount all files provided by a secret with a single ```VolumeMount``` entry
in the container specification.

### Secret API Resource

A new resource for secrets will be added to the API:

```go
type Secret struct {
    TypeMeta
    ObjectMeta

    // Keys in this map are the paths relative to the volume
    // presented to a container for this secret data.
    Data map[string][]byte
    Type SecretType
}

type SecretType string

const (
    SecretTypeOpaque              SecretType = "opaque"           // Opaque (arbitrary data; default)
    SecretTypeKubernetesAuthToken SecretType = "kubernetes-auth"  // Kubernetes auth token
    SecretTypeKerberosKeytab      SecretType = "kerberos-keytab"  // Kerberos keytab
    // FUTURE: other type values
)

const MaxSecretSize = 1 * 1024 * 1024
```

A Secret can declare a type in order to provide type information to system components that handle
exposing secrets to pods.  The default type is `opaque`, which represents arbitrary user-owned
data.

Secrets are validated against `MaxSecretSize`.

A new REST API and registry interface will be added to accompany the `Secret` resource.  The
default implementation of the registry will store `Secret` information in etcd.  Future registry
implementations could store the `TypeMeta` and `ObjectMeta` fields in etcd and store the secret
data in another data store entirely, or store the whole object in another data store.

### Secret Volume Source

A new `SecretSource` type of volume source will be added to the ```VolumeSource``` struct in the
API:

```go
type VolumeSource struct {
    // Other fields omitted

    // SecretSource represents a secret that should be presented in a volume
    SecretSource *SecretSource `json:"secret"`
}

type SecretSource struct {

    Target ObjectReference
}
```

### Secret Volume Plugin

The secret volume plugin would implement the actual retrieval and laying down of secrets on the
Kubelet's file system. Secrets may be stored in a special secret registry, as Docker volumes, LDAP
etc. See [Issue #2030](https://github.com/GoogleCloudPlatform/kubernetes/issues/2030) for options.
The implementation of this plugin is outside of the scope of this proposal. A default Etcd-based
version will be provided out of the box, but different solutions may be appropriate based on
specific user use-cases.

### Security Concerns

This proposal requires that secrets be placed on a filesystem location that is accessible to the
container requiring the secret. This makes it vulnerable to attacks on the container and the node,
especially if the secret is placed in plain text. MCS labels can mitigate some of this risk.
If there is a particular use case for a very sensitive secret, the secret itself could be
stored encrypted and placed in encrypted form in the file system for the container. The container
would have to know how to decrypt it and would receive the decryption key via another channel.
