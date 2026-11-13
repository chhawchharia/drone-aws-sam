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
| CONTAINER_ENV_VAR_FILE <span style="font-size: 10px"><br/>`string`</span>  |                                                   | Path to a mounted SAM container environment JSON file.             |
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

Build options are sent as JSON over an anonymous stdin pipe to the Python console
entry point installed by AWS SAM CLI. The original options exist only in Python's
in-memory argument list; their values are absent from the OS process arguments.
SAM's own Click parser handles all aliases, variable precedence, environment JSON
files (including `null`), and TOML/YAML configuration. The plugin does not create
temporary credential files or parse or rewrite `samconfig`.

`--save-params` retains native SAM behavior, including saving before the build
runs. SAM controls the original file's formatting, permissions and ownership.
Explicitly saving parameters can persist inline credentials even if the build
subsequently fails. SIGINT/SIGTERM cancel the running child through the plugin's
signal-aware context. SIGKILL cannot be handled, but the plugin has no temporary
credential file to leave behind.

The consumed `PLUGIN_PRIVATE_REGISTRY_PASSWORD` and
`PLUGIN_BUILD_COMMAND_OPTIONS` settings are unset after startup and filtered from
Docker and SAM child environments. **Unsetting a variable does not erase Linux's
initial `/proc/<pid>/environ`.** Legacy inline settings can still be read there by
processes with sufficient access. To avoid putting build variables in the
plugin's initial environment, mount a protected JSON secret file and set
`CONTAINER_ENV_VAR_FILE` to its path. Keep that mounted file outside saved build
artifacts and manage its lifecycle in the pipeline's secret mount.

These changes address command-argument exposure. Docker's unencrypted credential
storage warning is separate: use a Docker credential helper/store if credentials
must not be saved as reversible data in Docker's `config.json`. Build variables
remain environment variables in the build container and can be inspected by
processes with sufficient Docker daemon or container access. Builds and their
tools must continue to avoid printing secret values.

### Development

Building this module requires Go 1.21 or later. Run `go test ./...` and
`go test -race ./...` to check credential transport and signal cancellation.
Python 3.10 or later is required for the transport tests. Custom plugin images
must provide `python3` with `aws-sam-cli` installed, as the amd64 and arm64
Dockerfiles do.

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
