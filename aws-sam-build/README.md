# drone-aws-sam-build

- [Synopsis](#Synopsis)
- [Parameters](#Parameters)
- [Notes](#Notes)
- [Plugin Image](#Plugin-Image)
- [Examples](#Examples)

## Synopsis

This plugin enables building AWS Serverless Application Model (SAM) applications using the `sam build` command. It supports various options for building, including using a specific Docker image, providing build command options, and authenticating with a private Docker registry.

## Parameters

| Parameter                                                                    | Choices/<span style="color:blue;">Defaults</span> | Comments                                                          |
| :--------------------------------------------------------------------------- | :------------------------------------------------ | ----------------------------------------------------------------- |
| BUILD_IMAGE <span style="font-size: 10px"><br/>`string`</span>               |                                                   | The Docker image to use for building the SAM application.         |
| TEMPLATE_FILE_PATH <span style="font-size: 10px"><br/>`string`</span>        | `template.yaml`/`template.yml`                    | The path to the SAM template file.                                |
| BUILD_COMMAND_OPTIONS <span style="font-size: 10px"><br/>`string`</span>     |                                                   | Additional options for the `sam build` command.                   |
| PRIVATE_REGISTRY_URL <span style="font-size: 10px"><br/>`string`</span>      |                                                   | The URL of the private Docker registry.                           |
| PRIVATE_REGISTRY_USERNAME <span style="font-size: 10px"><br/>`string`</span> |                                                   | The username for authenticating with the private Docker registry. |
| PRIVATE_REGISTRY_PASSWORD <span style="font-size: 10px"><br/>`string`</span> |                                                   | The password for authenticating with the private Docker registry. |

## Notes

- If `BUILD_IMAGE` is provided, the `sam build` command will use the specified Docker image for building the SAM application.
- If you're using a private image, provide `PRIVATE_REGISTRY_URL`, `PRIVATE_REGISTRY_USERNAME`, and `PRIVATE_REGISTRY_PASSWORD`.
- If you're running this plugin in a kubernetes pod and using a custom docker build image, make sure to mount the path `/var/run/docker.sock`.

### Credential handling

The existing plugin settings are unchanged. Registry login supplies the password
to `docker login --password-stdin`; the password is not a Docker command argument.

Inline build variables supplied through `BUILD_COMMAND_OPTIONS` using `-e` or
`--container-env-var` are moved into a temporary SAM JSON environment file. The
plugin passes only its path using `--container-env-var-file`. The file has mode
`0600` inside a private `0700` directory and is removed when the build returns,
including on login failure, build failure, or context cancellation. The original
`PLUGIN_PRIVATE_REGISTRY_PASSWORD` and `PLUGIN_BUILD_COMMAND_OPTIONS` settings are
not inherited by Docker and SAM subprocesses.

Existing environment JSON files and TOML/YAML `samconfig` files are supported.
Variables retain SAM's precedence: resource-specific inline values, resource-
specific file values, global inline values, then global file values. Existing
files are not rewritten. When necessary, a temporary copy of the selected SAM
configuration prevents shadowed config variables from becoming active after the
inline command arguments are removed.

`--save-params` and configuration-based `save_params` remain supported. SAM saves
against the private config copy; the plugin restores the original inline option
values and environment-file selection before saving the result at the original
config location with mode `0600`. No saved parameter references the temporary
credential file. As with SAM's existing behavior, explicitly saving parameters
also persists any inline credential values in that configuration file.

These changes address command-argument exposure. Docker's unencrypted credential
storage warning is separate: use a Docker credential helper/store if credentials
must not be saved as reversible data in Docker's `config.json`. Build variables
remain environment variables in the build container and can be inspected by
processes with sufficient Docker daemon or container access. Builds and their
tools must continue to avoid printing secret values.

### Development

Building this module requires Go 1.21 or later. Run `go test ./...` and
`go test -race ./plugin` to check the plugin, including its credential transport
and temporary-file lifecycle.

## Plugin Image

The plugin `plugins/aws-sam-build` is available for the following architectures:

| OS          | Tag      |
| ----------- | -------- |
| linux-amd64 | `latest` |

## Examples

```
    - step:
        type: Plugin
        name: aws-sam-build
        identifier: sam_plugin
        spec:
                connectorRef: <connector>
                image: plugins/aws-sam-build:latest
                settings:
                    template_file_path: template.yaml
                    build_image: public.ecr.aws/sam/build-python3.9:1.112.0-20240313001230


    - step:
        type: Plugin
        name: aws-sam-build
        identifier: sam_plugin
        spec:
                connectorRef: <connector>
                image: plugins/aws-sam-build:latest
                settings:
                    template_file_path: template.yaml

    - step:
        type: Plugin
        name: aws-sam-build
        identifier: sam_plugin
        spec:
                connectorRef: <connector>
                image: plugins/aws-sam-build:latest
                settings:
                    template_file_path: template.yaml
                    build_image: image
                    private_registry_url: registry-url
                    private_registry_username: username
                    private_registry_password: password
```
