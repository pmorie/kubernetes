# Build example

### Create build images

TODO: steps

### Start a build

Example build JSON:

```json
{
  "kind": "Build",
  "apiVersion": "v1beta1",
  "config": {
    "type": "docker",
    "sourceUri": "https://raw.githubusercontent.com/orchardup/fig/master/tests/fixtures/simple-dockerfile/Dockerfile",
    "imageTag": "my-built-image"
  }
}
```
