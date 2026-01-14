# Scripts

Helper scripts for using Shelley with exe.dev.

## describe-image

Analyze images from your local machine using the vision model on your exe.dev VM.

### Installation

```bash
# Copy to your local machine
scp <your-vm>.exe.xyz:/home/exedev/shelley-cli/scripts/describe-image ~/.local/bin/
chmod +x ~/.local/bin/describe-image
```

### Usage

```bash
# With -v flag (recommended)
describe-image -v myvm screenshot.png
describe-image -v myvm photo.jpg "What's in this image?"

# Or set environment variable
export EXE_DEV_HOST=myvm.exe.xyz
describe-image screenshot.png
```

### Integration with Shelley CLI

After running `describe-image`, the result is saved on the VM. In your Shelley CLI session:

```
/imglist          # List available image descriptions
/imgresult        # Inject most recent description into chat
/imgresult 2      # Inject a specific result
```

This gives the model context about images from your local machine.

### Requirements

- SSH access to your exe.dev VM
- Shelley built on the VM with `/exe.dev/shelley.json` config
- `file` command on your local machine (standard on macOS/Linux)
