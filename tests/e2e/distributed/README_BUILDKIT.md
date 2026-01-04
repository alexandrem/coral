# BuildKit Configuration for E2E Tests

The E2E tests use Docker BuildKit's cache mounts to dramatically speed up Go module downloads. This requires BuildKit to be enabled.

## Quick Setup

### Option 1: Set Environment Variable (Recommended)

Add to your shell profile (`~/.zshrc`, `~/.bashrc`, etc.):

```bash
export DOCKER_BUILDKIT=1
```

Then reload your shell or run:
```bash
source ~/.zshrc  # or ~/.bashrc
```

### Option 2: Set Per-Command

Run tests with BuildKit enabled:

```bash
DOCKER_BUILDKIT=1 make test-e2e
```

### Option 3: Enable BuildKit in Colima (Permanent)

If using Colima, enable BuildKit by default:

```bash
# Edit Colima config
colima ssh

# Inside Colima VM
sudo mkdir -p /etc/docker
echo '{"features": {"buildkit": true}}' | sudo tee /etc/docker/daemon.json
sudo systemctl restart docker

# Exit Colima VM
exit
```

## Verify BuildKit is Enabled

```bash
# Should show "1" if enabled
echo $DOCKER_BUILDKIT

# Or test a build
docker build --help | grep -i buildkit
```

## Performance Impact

**Without BuildKit cache:**
- Build time: ~5-10 minutes per container
- Total E2E test time: ~30-45 minutes

**With BuildKit cache (after first run):**
- Build time: ~30-60 seconds per container
- Total E2E test time: ~10-15 minutes ⚡

## Troubleshooting

**Error: "the --mount option requires BuildKit"**

This means BuildKit is not enabled. Follow Option 1 or 2 above.

**Cache not persisting between runs**

BuildKit caches are managed by Docker. Verify with:
```bash
docker system df -v | grep -i cache
```

**Want to disable BuildKit temporarily?**

```bash
DOCKER_BUILDKIT=0 make test-e2e
```

Note: Tests will still work but builds will be slower.
