**Mitte** is a personal PaaS (Platform-as-a-Service) implemented in a single, modern Go binary. It allows you to transform any server into your own private cloud deployment platform.

Using the power of Docker, Git, and Caddy, `mitte` provides a simple, self-hosted deployment workflow for any application. Deploy from source code using `git push` (with automatic building via Dockerfiles) or deploy pre-built Docker images directly for maximum flexibility and speed.

### ✨ Features

- 🚀 **Git Push to Deploy**: The classic Heroku workflow you know and love.
- 📦 **Single Go Binary**: Incredibly easy to install and manage. No complex dependency chains.
- 🔒 **Automatic HTTPS**: Caddy provides free, managed SSL certificates for all your apps, out-of-the-box.
- 🏗️ **Dockerfile Support**: Automatically builds your application using your existing `Dockerfile`.
- 📦 **Buildpack Support**: Deploy applications without Dockerfiles using Cloud Native Buildpacks (CNB) for automatic language detection and building.
- 🐳 **Pre-built Image Support**: Deploy any Docker image directly without building from source code.
- ⚙️ **Comprehensive CLI**: A powerful, easy-to-use command-line interface for managing the full lifecycle of your apps: configuration, domains, logs, and more.
- 🛡️ **Secure by Design**: Runs operations through a dedicated, unprivileged `mitte` user on the host.
- 🗄️ **MariaDB Support**: Built-in MariaDB database service with health checks, backup/restore, and custom configuration support.
- 🐘 **PostgreSQL Support**: Built-in PostgreSQL database service with health checks, backup/restore, and user management.
- 🔄 **Automatic Route Recovery**: Container watcher service automatically fixes Caddy routes after Docker restarts.
- 🛠️ **Route Management Tools**: Commands to manually fix broken routes and manage the watcher service.

### How It Works

Mitte listens for `git push` commands over SSH. When it receives a push for an app, it follows one of three deployment paths based on configuration priority:

#### Source Code Deployment with Dockerfile (Default)

1.  **Receives** the source code in a bare git repository.
2.  **Builds** the code into a Docker image using your `Dockerfile`.
3.  **Runs** the image as a new container.
4.  **Routes** traffic to the new container by dynamically updating its Caddy reverse proxy via Caddy's admin API.

#### Source Code Deployment with Buildpacks

1.  **Receives** the source code in a bare git repository.
2.  **Detects** the application language and framework automatically.
3.  **Builds** the application using Cloud Native Buildpacks (CNB) without requiring a `Dockerfile`.
4.  **Runs** the built image as a new container.
5.  **Routes** traffic to the new container.

#### Pre-built Image Deployment

1.  **Receives** the git push (for triggering deployment).
2.  **Skips** the build process entirely.
3.  **Pulls** the pre-configured Docker image.
4.  **Runs** the image as a new container with custom volumes, ports, and environment variables.
5.  **Routes** traffic to the new container.

**⚠️ Important Note**: When using pre-built images, environment variables are applied at runtime, not during the image build process. This means environment variables will be available to your running container, but cannot be used during the image build phase. If your application requires environment variables during the build process (e.g., `DATABASE_URL` for database migrations), use the source code deployment method with a Dockerfile instead.

It's a simple, robust system that takes your code or pre-built images from commit to a running, publicly accessible application in seconds.

### 🏁 Getting Started

Transform your fresh server into a `mitte` host.

**1. Run the Setup on Your Server**

SSH into your server as root and run the installer. This script downloads the latest `mitte` binary and runs the `mitte setup` command for you.

```bash
# Run this on your DEDICATED server
ssh root@your-server.com
curl -sSL https://mitte.sh/install.sh | bash
```

**2. Add Your Public SSH Key**

On your local machine, add your SSH key to the `mitte` user on the server. This authorizes you to push code.

```bash
# Run this on your LOCAL machine
cat ~/.ssh/id_rsa.pub | ssh root@your-server.com "mitte keys add my-laptop"
```

**3. Deploy Your First App**

In your local git repository, add a `mitte` remote and push!

```bash
# In your project directory
git remote add mitte mitte@your-server.com:my-awesome-app
git push mitte main
```

**That's it!** Your application is now deployed at `http://my-awesome-app.your-server.com`.

> **Note**: Mitte supports three deployment modes:
>
> - **Source Code with Dockerfile**: Push your code with a `Dockerfile` and let Mitte build it automatically
> - **Source Code with Buildpacks**: Push your code and let Mitte detect your language and build using Cloud Native Buildpacks
> - **Pre-built Images**: Configure a Docker image and deploy it directly (see the Pre-built Images section below)

### 🐳 Deploying Pre-built Images

Mitte also supports deploying pre-built Docker images directly, perfect for applications that are already containerized or for faster deployments.

**⚠️ Environment Variable Limitation**: When using pre-built images, environment variables are applied at runtime, not during image build. This means:

- ✅ Environment variables will be available to your running container
- ❌ Environment variables cannot be used during the image build process
- 🔄 If you need environment variables during build (e.g., for database migrations), use the source code deployment method with a Dockerfile instead

**1. Create and Configure Your App**

```bash
# Create the app
ssh root@your-server.com "mitte apps create my-image-app"

# Set the Docker image
ssh root@your-server.com "mitte apps set-image my-image-app nginx:latest"

# Configure volumes (optional)
ssh root@your-server.com "mitte apps set-volumes my-image-app /host/path:/container/path"

# Configure ports (optional)
ssh root@your-server.com "mitte apps set-ports my-image-app 8080:80"

# Configure custom command (optional)
ssh root@your-server.com "mitte apps set-command my-image-app serve"

# Configure custom user (optional)
ssh root@your-server.com "mitte apps set-user my-image-app 1000:1000"

# Deploy the image
ssh root@your-server.com "mitte apps deploy-image my-image-app"
```

**2. Deploy via Git Push (Alternative)**

You can also trigger deployments of pre-built images using git push:

```bash
# In your project directory (even if empty)
git init
git remote add mitte mitte@your-server.com:my-image-app
git push mitte main
```

The system will detect the pre-built image configuration and deploy it directly without building.

### 📦 Deploying with Buildpacks

Mitte supports deploying applications using Cloud Native Buildpacks (CNB), which automatically detect your application's language and framework, eliminating the need for a `Dockerfile`. This is perfect for standard applications in popular languages.

**Note**: Buildpack support requires the `pack` CLI (v0.39.0 or later) to be installed on your server. The `mitte setup` command automatically installs the latest compatible version. If you're using Docker 29.x or later, you need pack v0.39.0+ for Docker API compatibility.

**1. Configure Buildpack for Your App**

```bash
# Create the app
ssh root@your-server.com "mitte apps create my-buildpack-app"

# Set a specific buildpack (optional - auto-detection will be used if not set)
ssh root@your-server.com "mitte apps set-buildpack my-buildpack-app paketobuildpacks/nodejs"

# Configure environment variables (optional)
ssh root@your-server.com "mitte config set my-buildpack-app NODE_ENV=production"
```

**2. Deploy via Git Push**

```bash
# In your project directory
git remote add mitte mitte@your-server.com:my-buildpack-app
git push mitte main
```

Mitte will automatically detect your application type based on files like `package.json`, `requirements.txt`, `go.mod`, etc., and build it using the appropriate buildpack.

**3. Supported Languages and Frameworks**

Mitte can automatically detect and build applications in:

- **Node.js** (`package.json`)
- **Python** (`requirements.txt`, `Pipfile`, `pyproject.toml`)
- **Go** (`go.mod`, `go.sum`, `main.go`)
- **Java** (`pom.xml`, `build.gradle`, `build.gradle.kts`)
- **.NET** (`*.csproj`, `*.fsproj`, `project.json`)
- **Ruby** (`Gemfile`)
- **PHP** (`composer.json`)

### 📖 Command Reference

Mitte comes with a powerful command-line interface to manage all aspects of your applications. All commands are run on your server (e.g., by running `ssh root@your-server.com "mitte <command>"`).

#### App Management

Manage your applications.

```bash
# List all deployed applications
mitte apps list

# Create a new, empty application
# This is useful for configuring an app before the first push
mitte apps create <appname>

# Set a pre-built Docker image for an app
mitte apps set-image <appname> <image>

# Configure volume mounts for an app
mitte apps set-volumes <appname> /host/path:/container/path

# Configure port mappings for an app
mitte apps set-ports <appname> 8080:80

# Configure a custom command/entrypoint for an app
mitte apps set-command <appname> serve --port 8080

# Configure a custom user (UID:GID) to run the container
mitte apps set-user <appname> 1000:1000

# Deploy a pre-built image (after configuration)
mitte apps deploy-image <appname>

# Set the buildpack to use for an app
mitte apps set-buildpack <appname> <buildpack-id>

# Detect and suggest a buildpack for an application
mitte apps detect-buildpack <appname>

# Enable an application
mitte apps enable <appname>

# Disable an application
mitte apps disable <appname>

# Permanently destroy an application and all its resources
mitte apps destroy <appname>
```

#### Configuration (Env Vars)

Manage environment variables for a specific application. Changes take effect by restarting the app's container unless `--no-restart` is specified.

```bash
# List all environment variables for an app
mitte config list <appname>

# Set one or more environment variables
mitte config set <appname> DATABASE_URL=... SECRET_KEY=...

# Set a variable witout restarting the application
mitte config set <appname> KEY=VALUE --no-restart

# Unset one or more environment variables
mitte config unset <appname> SECRET_KEY

# Bulk edit environment variables in a text editor
# This method preserves comments and the order of variables
mitte config edit <appname>
```

#### Domain Management

Manage custom domains for an application. Mitte will automatically provision SSL certificates for all domains.

```bash
# List all domains for an app
mitte domains list <appname>

# Add a domain to an app
mitte domains add <appname> www.my-awesome-app.com

# Remove a domain from an app
mitte domains remove <appname> www.my-awesome-app.com
```

#### Log Management

View the logs of a running application.

```bash
# Show the most recent logs
mitte logs <appname>

# Show the last 100 log lines
mitte logs <appname> -n 100

# Follow the log output in real-time
mitte logs <appname> --follow
# or using the shorthand
mitte logs <appname> -f

# Follow the last 100 lines
mitte logs <appname> -f -n 100
```

#### Database Management (MariaDB)

Manage MariaDB database instances for your applications.

```bash
# List all MariaDB instances
mitte mariadb list

# Create a new MariaDB instance
mitte mariadb create <instance-name> [--version=latest] [--database=name] [--user=username] [--password=password] [--config-file=path] [--data-dir=path] [--max-connections=N] [--thread-cache-size=N] [--table-open-cache=N] [--innodb-buffer-pool-size=SIZE] [--query-cache-size=SIZE] [--pooling-preset=PRESET]

# Example: Create a MariaDB instance with version 10.11 and initial database 'myapp'
mitte mariadb create mydb --version=10.11 --database=myapp

# Example: Create a MariaDB instance with a custom user
mitte mariadb create mydb --user=myuser --database=myapp

# Example: Create a MariaDB instance with custom configuration
mitte mariadb create mydb --config-file=/path/to/my-config.cnf

# Example: Create a MariaDB instance with a custom host data directory
mitte mariadb create mydb --data-dir=/docker/joplindb

# Example: Create with connection pooling preset
mitte mariadb create mydb --pooling-preset=high-traffic --version=10.11

# Example: Create with specific pooling configuration
mitte mariadb create mydb --max-connections=500 --thread-cache-size=100 --innodb-buffer-pool-size=2G

# Link a MariaDB instance to an app (sets DATABASE_URL)
mitte mariadb link <instance-name> <app-name> [env-var-name]

# This sets a DATABASE_URL environment variable in the format: mysql://user:password@instance:3306/database

# Create a backup of all databases in a MariaDB instance
mitte mariadb backup <instance-name> <output-file>

# Example: Backup to a local file
mitte mariadb backup mydb /backups/mydb-backup.sql

# Restore databases from a backup file
mitte mariadb restore <instance-name> <backup-file>

# Example: Restore from backup
mitte mariadb restore mydb /backups/mydb-backup.sql

# Manage database users
mitte mariadb users create <instance-name> <username> [--password=...] [--database=...] [--privileges=...]
mitte mariadb users delete <instance-name> <username>
mitte mariadb users list <instance-name>

# Example: Create a read-only user
mitte mariadb users create mydb readonly --database=mydata --privileges=SELECT

# Example: Create a user with full privileges
mitte mariadb users create mydb appuser --database="*" --privileges="ALL PRIVILEGES"

# Database version upgrades
mitte mariadb upgrade check <instance-name>
mitte mariadb upgrade perform <instance-name> --to-version=<version> [--dry-run]

# Example: Check upgrade status
mitte mariadb upgrade check mydb

# Example: Perform upgrade to version 10.11
mitte mariadb upgrade perform mydb --to-version=10.11

# Example: Dry run upgrade to latest version
mitte mariadb upgrade perform mydb --to-version=latest --dry-run

# Connection pooling management
mitte mariadb connections stats <instance-name>
mitte mariadb connections analyze <instance-name>
mitte mariadb connections optimize <instance-name> [--preset=small|medium|large|high-traffic]
mitte mariadb connections apply <instance-name> [--config-file=path] [--max-connections=N] [--thread-cache-size=N] [--table-open-cache=N] [--innodb-buffer-pool-size=SIZE] [--query-cache-size=SIZE] [--pooling-preset=PRESET] [--restart]

# Example: Show connection statistics
mitte mariadb connections stats mydb

# Example: Analyze connection usage
mitte mariadb connections analyze mydb

# Example: Optimize with specific preset
mitte mariadb connections optimize mydb --preset=high-traffic

# Example: Apply pooling configuration from file
mitte mariadb connections apply mydb --config-file=/path/to/pooling.cnf --restart

# Example: Apply pooling configuration with flags
mitte mariadb connections apply mydb --max-connections=500 --thread-cache-size=100 --innodb-buffer-pool-size=2G --restart

# Example: Apply pooling preset
mitte mariadb connections apply mydb --pooling-preset=high-traffic --restart

# Permanently destroy a MariaDB instance and its data
mitte mariadb destroy <instance-name>
```

#### Database Management (PostgreSQL)

Manage PostgreSQL database instances for your applications.

```bash
# List all PostgreSQL instances
mitte postgres list

# Create a new PostgreSQL instance
mitte postgres create <instance-name> [--version=latest] [--database=name] [--user=username] [--password=password] [--data-dir=path]

# Example: Create a PostgreSQL instance with version 16 and initial database 'myapp'
mitte postgres create mydb --version=16 --database=myapp

# Example: Create a PostgreSQL instance with a custom host data directory
mitte postgres create mydb --data-dir=/docker/postgres-data

# Link a PostgreSQL instance to an app (sets DATABASE_URL)
mitte postgres link <instance-name> <app-name> [env-var-name]

# This sets a DATABASE_URL environment variable in the format: postgres://user:password@instance:5432/database

# Create a backup of a PostgreSQL instance
mitte postgres backup <instance-name> <output-file>

# Restore databases from a backup file
mitte postgres restore <instance-name> <backup-file>

# Manage database users
mitte postgres users create <instance-name> <username> [--password=...] [--database=...]
mitte postgres users delete <instance-name> <username>
mitte postgres users list <instance-name>

# Permanently destroy a PostgreSQL instance and its data
mitte postgres destroy <instance-name>
```

##### MariaDB Features

**Health Checks**: All MariaDB containers include automatic health checks that:

- Run `mysqladmin ping` every 10 seconds after a 30-second startup period
- Mark containers unhealthy after 3 consecutive failures
- Enable automatic restarts for reliable database operation

**Backup/Restore**: Full database protection with:

- Complete SQL dumps of all databases using `mysqldump`
- Secure password retrieval from service state
- Easy restoration from backup files

**Custom Configuration**: Advanced database tuning with:

- Mount custom `.cnf` files into `/etc/mysql/conf.d/custom.cnf`
- Support for performance tuning, security settings, and production requirements
- Read-only mounting for security

**Custom Data Directory**: Store your data where you want:

- Use `--data-dir=/path/to/data` to mount a specific host directory
- Data persists on the host even if the instance is destroyed (safety feature)
- Default behavior uses managed Docker volumes for convenience

**Version Upgrades**: Safe database migration with:

- Automatic backup creation before upgrade
- In-place version upgrades with data preservation
- Dry-run mode for testing upgrades
- Automatic `mysql_upgrade` execution when needed
- Rollback tracking with previous version history

**Connection Pooling**: Optimized database performance with:

- Preset configurations (small, medium, large, high-traffic)
- Automatic connection usage analysis and statistics
- Intelligent configuration recommendations with resource detection
- Apply configuration to existing instances without recreation
- Support for granular pooling parameters:
  - `max_connections`: Maximum concurrent connections
  - `thread_cache_size`: Threads cached for reuse
  - `table_open_cache`: Table descriptors cached
  - `innodb_buffer_pool_size`: InnoDB memory allocation (auto-calculated from container limits)
  - `query_cache_size`: Query result caching
- Advanced features:
  - Container memory limit detection for optimal buffer pool sizing
  - Connection churn analysis to identify thread cache issues
  - Live configuration reload (SIGHUP) without container restart
  - Safe container restart option for parameters requiring full restart

**Connection Pooling Commands in Detail**:

1. **`mitte mariadb connections stats <instance-name>`**

   - Shows current connection statistics without analysis
   - Displays: Max Connections, Threads Connected, Threads Running, Threads Cached, Threads Created, Connection Usage %, Connection Churn

2. **`mitte mariadb connections analyze <instance-name>`**

   - Analyzes connection usage and provides recommendations
   - Identifies: High connection usage, connection churn issues, long-running queries
   - Suggests configuration improvements based on current usage patterns

3. **`mitte mariadb connections optimize <instance-name> [--preset]`**

   - Generates optimized configuration based on current usage or specified preset
   - Auto-detects container memory limits for optimal buffer pool sizing
   - Provides configuration that can be saved to a file and applied

4. **`mitte mariadb connections apply <instance-name> [options] [--restart]`**
   - Applies pooling configuration to existing instances
   - Can use config file or command-line parameters
   - Supports live reload (SIGHUP) without restart for most parameters
   - `--restart` flag for parameters requiring full container restart

**Pooling Presets**:

- **small**: 100 connections, 8 thread cache, 256M buffer pool (development)
- **medium**: 200 connections, 50 thread cache, 1G buffer pool (standard production)
- **large**: 300 connections, 75 thread cache, 2G buffer pool (high-concurrency)
- **high-traffic**: 500 connections, 100 thread cache, 4G buffer pool, optimized timeouts (enterprise)

**Resource Detection**:

- Auto-detects container memory limits from Docker
- Calculates optimal InnoDB buffer pool size (75% of available RAM)
- Ensures sufficient memory for OS and other processes
- Falls back to preset defaults if detection fails

**Example Configuration File** (`my-performance.cnf`):

```ini
[mariadbd]
# Performance tuning
innodb_buffer_pool_size = 1G
max_connections = 200
query_cache_size = 128M

# Connection optimization
thread_cache_size = 50
table_open_cache = 2000

# Monitoring
slow_query_log = 1
long_query_time = 2
```

##### PostgreSQL Features

**Health Checks**: All PostgreSQL containers include automatic health checks that:

- Run `pg_isready` every 10 seconds after a 30-second startup period
- Mark containers unhealthy after 5 consecutive failures
- Enable automatic restarts for reliable database operation

**Backup/Restore**: Full database protection with:

- Complete SQL dumps of all databases using `pg_dumpall`
- Secure password retrieval from service state
- Easy restoration from backup files using `psql`

**Custom Data Directory**: Store your data where you want:

- Use `--data-dir=/path/to/data` to mount a specific host directory
- Data persists on the host even if the instance is destroyed (safety feature)
- Default behavior uses managed Docker volumes for convenience

**User Management**: Easy database access control:

- Create and delete database users with specific database access
- Automatic secure password generation
- List existing users

#### Access Management (SSH Keys)

Manage the public SSH keys that are authorized to deploy applications.

```bash
# NOTE: These commands are typically run via SSH as shown in the "Getting Started" guide.
# ssh root@your-server.com "mitte keys ..."

# Add a new SSH key from stdin
# cat ~/.ssh/id_rsa.pub | ssh root@your-server.com "mitte keys add <key-name>"

# List all authorized SSH keys
# ssh root@your-server.com "mitte keys list"

# Remove an SSH key by its name
# ssh root@your-server.com "mitte keys remove <key-name>"
```

#### Host & Project Management

Utility commands for setting up the Mitte host and local project remotes.

```bash
# Initialize the Mitte host (run on the server)
# This is handled automatically by the install script.
mitte setup

# Add a git remote to your local project (run on your local machine)
# Replaces 'git remote add ...'
mitte remote --host your-server.com --app my-awesome-app
```

### 🛠️ Troubleshooting

#### Caddy "Connection Refused" Errors After Docker Restart

If you see errors like `dial tcp 127.0.0.1:33188: connect: connection refused` in Caddy logs after Docker restarts, this is because containers get new random ports. Mitte includes several solutions:

**Immediate Fix:**

```bash
# Fix all broken routes
sudo mitte fix-routes

# Or restart a specific app
sudo mitte apps restart <appname>
```

**Automatic Prevention:**
The setup process installs a container watcher service that automatically detects port changes and updates Caddy. You can manage it with:

```bash
# Check watcher status
sudo mitte watcher status

# Restart the watcher
sudo mitte watcher restart

# View watcher logs
sudo mitte watcher logs
```

**Manual Route Management:**

```bash
# List all apps and their current ports
sudo mitte apps list

# Check if an app's route exists
sudo mitte routes check <appname>

# Manually update an app's route
sudo mitte routes update <appname> <port>
```

#### Buildpack Issues with Docker 29.x+

If you encounter Docker API version errors with buildpacks, ensure you have pack CLI v0.39.0+ installed:

```bash
# The setup command installs the correct version automatically
sudo mitte setup
```

### 💖 Contributing

We would love your help to make `mitte` even better! If you're interested, please see the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines on how to get started.

### 📜 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

[mitte.sh](https://mitte.sh)
