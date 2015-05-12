# Variable expansion in pod command, args, and env

## Abstract

A proposal for the expansion of environment variables using a simple `$(var)` syntax.

## Motivation

It is extremely common for users to need to compose environment variables or pass arguments to
their commands using the values of environment variables.  Currently, Kubernetes does not perform
any expansion of varibles.  The work around is to invoke a shell in the container's command have the
shell perform the substitution, or to write a wrapper script that sets up the environment and runs
the command.  This has a number of drawbacks:

1.  Solutions that require a shell are unfriendly to images that do not contain a shell
2.  Wrapper scripts make it harder to use images as base images
3.  Wrapper scripts increase coupling to kubernetes

## Goals

1.  Define the syntax format
2.  Define the scoping and ordering of substitutions
3.  Define the behavior for unmatched variables
4.  Define the behavior for unexpected/malformed input

## Constraints and Assumptions

*  This design should describe the simplest possible syntax to accomplish the use-cases
*  Expansion syntax will never support more complicated shell-like behaviors such as default values
   (viz: `$(VARIABLE_NAME:"default")`), inline substitution, etc.

## Use Cases

1.  As a user, I want to compose new environment variables for a container using a substitution
    syntax.
1.  As a user, I want to substitute environment variables into a container's comman
1.  As a user, I want to do the above without requiring the container's image to have a shell

## Design Considerations

### What should the syntax be?

The exact syntax for variable expansion has a large impact on how users perceive and relate to the
feature.  We considered implementing a very restrictive subset of the shell `${var}` syntax.  This
is an attractive option on some level, because many people are familiar with this syntax.  However,
this syntax also has a large number of lesser known features such as the ability to provide
default values for unset variables, perform inline substitution, etc.

### No new features

TODO: we will not support additional features

## Proposed Design

### Expansion mechanics

#### Syntax

#### Scope and ordering of substitutions

#### Unmatched variables

#### Unexpected input

#### Malformed input

### Examples

#### Example: Building a URL

#### Example: Default value for service variable

### Implementation changes

#### `pkg/util/expansion` package

#### Kubelet `makeEnvVars` changes

#### Kubelet `runContainer` changes

