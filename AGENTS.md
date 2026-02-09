# Agent Instructions for Mitte App

## Build Commands

- Build: `go build -o mitte .`
- Build and deploy: `./build.sh`

## Test Commands

- No tests currently implemented

## Code Style Guidelines

### Imports

- Group imports: standard library, third-party, local packages
- Use blank lines between import groups

### Naming Conventions

- Exported functions/types: PascalCase (e.g., `Deploy`, `App`)
- Local variables/functions: camelCase (e.g., `appName`, `containerName`)
- Constants: PascalCase (e.g., `appsDir`)

### Error Handling

- Use `fmt.Errorf` with `%w` for error wrapping
- Return errors early, don't ignore them unless intentional
- Use `defer` for cleanup operations

### Formatting

- Use `gofmt` for consistent formatting
- Use `json.MarshalIndent` with 2 spaces for JSON output
- Use `fmt.Fprintf(os.Stderr, ...)` for user-facing output

### Types and Structs

- Use JSON tags for serialization: `json:"field_name"`
- Define clear struct types for return values (e.g., `DeployResult`)

### Context Usage

- Pass `context.Context` to functions that may be cancelled
- Use context for Docker API calls

### Dependencies

- Uses Cobra for CLI framework
- Uses Docker client libraries
- Go version: 1.24.4

## Application Management Commands

### Core Commands

- `mitte apps create <app-name>` - Create a new application placeholder
- `mitte apps list` - List all deployed applications
- `mitte apps destroy <app-name>` - Permanently destroy an application
- `mitte apps build <app-name>` - Build and deploy from source code

### Database Services

#### MariaDB

- `mitte mariadb list` - List all MariaDB instances
- `mitte mariadb create <name>` - Create a new MariaDB instance
- `mitte mariadb destroy <name>` - Destroy a MariaDB instance
- `mitte mariadb link <instance> <app>` - Link MariaDB to an app
- `mitte mariadb backup/restore <instance> <file>` - Backup or restore data
- `mitte mariadb users` - Manage database users

#### PostgreSQL

- `mitte postgres list` - List all PostgreSQL instances
- `mitte postgres create <name>` - Create a new PostgreSQL instance
- `mitte postgres destroy <name>` - Destroy a PostgreSQL instance
- `mitte postgres link <instance> <app>` - Link PostgreSQL to an app
- `mitte postgres backup/restore <instance> <file>` - Backup or restore data
- `mitte postgres users` - Manage database users

### Pre-built Image Deployment

- `mitte apps set-image <app> <image>` - Set pre-built Docker image for an app
- `mitte apps deploy-image <app>` - Deploy an app using the configured pre-built image

### Advanced Configuration

- `mitte apps set-volumes <app> <volume>...` - Set volume mounts (host:container[:options])
- `mitte apps set-ports <app> <port>...` - Set port mappings (host:container)
- `mitte apps set-command <app> <command>...` - Set custom entrypoint/command
- `mitte apps set-user <app> <user>` - Set custom user (UID:GID) to run the container

### Buildpack Support

- `mitte apps set-buildpack <app> <buildpack-id>` - Manually set a buildpack
- `mitte apps detect-buildpack <app>` - Detect and suggest a buildpack for the app

### Environment Variables

- `mitte config set <app> KEY=VALUE` - Set environment variables
- `mitte config unset <app> KEY` - Remove environment variables
- `mitte config list <app>` - List current environment variables
- `mitte config edit <app>` - Bulk edit environment variables in an editor

## Implementation Details

### State Management

- App state is stored in `/var/lib/mitte/apps/<app-name>.json`
- Always use `pkg/state` to load and save app configuration

### Deployment Flow

1. Load app state
2. Stop and remove existing container
3. Parse volumes and port bindings
4. Create and start new container with `pkg/deployer`
5. Update Caddy routes using `pkg/router`
