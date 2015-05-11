# Variable expansion in pod command, args, and env

## Abstract

A proposal for the expansion of environment variables using a restricted form of shell `${var}`
syntax.

## Motivation

It is extremely common for users to need to compose environment variables or pass arguments to
their commands using the values of environment variables.  Currently, Kubernetes does not perform
any expansion of varibles.  The work around is to invoke a shell in the container's command have the
shell perform the substitution, or to write a wrapper script that sets up the environment and runs
the command.  This has a number of drawbacks:

1.  Solutions that require a shell are unfriendly to images that do not contain a shell
2.  Wrapper scripts make it harder to use images as base images
3.  Wrapper scripts increase coupling to kubernetes

Goals of this design:

1.  Define the syntax format
2.  Define the scoping and ordering of substitutions
3.  Define the behavior for unmatched variables
4.  Define the behavior for unexpected/malformed input

## Constraints and Assumptions

*  This design should describe the simplest possible syntax to accomplish the use-cases
*  Expansion syntax will never support more complicated shell-like behaviors such as default values
   (viz: `${VARIABLE_NAME:"default"}`), inline substitution, etc.

## Use Cases

TODO

## Design Considerations

TODO

### Which sigils?

TODO

### No New Features

TODO: we will not support additional features

## Proposed Design

TODO

## Example: Building a URL

TODO

## Example: Default value for service variable

TODO
