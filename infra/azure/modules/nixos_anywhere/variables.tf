variable "name" {
  description = "Machine name, used only in error messages."
  type        = string
}

variable "flake_path" {
  description = "Absolute path to the directory holding nix/flake.nix."
  type        = string
}

variable "flake_attr" {
  description = "nixosConfigurations attribute to install, e.g. \"host\" or \"edge\"."
  type        = string
}

variable "target_host" {
  description = "Address nixos-anywhere connects to. A private address when jump_host is set."
  type        = string
}

variable "target_user" {
  description = "User on the Ubuntu image nixos-anywhere logs in as. Must be root."
  type        = string
  default     = "root"

  validation {
    condition     = var.target_user == "root"
    error_message = "nixos-anywhere needs root on the install target; cloud-init opens it for exactly this step."
  }
}

variable "ssh_private_key_path" {
  description = "Operator private key matching one of authorized_keys. Never read into state."
  type        = string
}

variable "authorized_keys" {
  description = "Operator public keys written to /root/.ssh/authorized_keys on the installed system."
  type        = list(string)

  validation {
    condition     = length(var.authorized_keys) > 0
    error_message = "At least one operator public key is required or the installed system is unreachable."
  }
}

variable "jump_host" {
  description = "Address of the SSH jump host, or null to connect directly. Hosts have no public IP, so they are reached through the edge."
  type        = string
  default     = null
}

variable "jump_user" {
  description = "User on the jump host."
  type        = string
  default     = "root"
}

variable "jump_port" {
  description = "SSH port on the jump host. The edge's operator sshd is not on 22."
  type        = number
  default     = 2222
}

variable "post_install_port" {
  description = "sshd port of the installed NixOS system."
  type        = number
  default     = 22
}

variable "post_install_commands" {
  description = "Commands run over SSH after the reboot into NixOS."
  type        = list(string)
  default     = []
}

variable "post_install_triggers" {
  description = "Values that re-run post_install_commands without reinstalling the machine (the join token uses this)."
  type        = any
  default     = null
}

variable "build_on" {
  description = "nixos-anywhere --build-on: auto, local or remote."
  type        = string
  default     = "auto"

  validation {
    condition     = contains(["auto", "local", "remote"], var.build_on)
    error_message = "build_on must be auto, local or remote."
  }
}

variable "connect_timeout" {
  description = <<-EOT
    How long the remote-exec provisioners either side of the install wait for
    SSH. It bounds those handshakes only: the install is a local-exec, which
    takes no timeout at all, and install_deadline is what bounds that.
  EOT
  type        = string
  default     = "20m"
}

variable "triggers" {
  description = "Values that force a reinstall when they change (VM id, flake attribute)."
  type        = any
}

variable "bootstrap_user" {
  description = "The image's provisioning user, which cloud images always let in. Used only to wait for cloud-init and to put the operator keys on root before nixos-anywhere logs in as target_user."
  type        = string
  default     = "azureuser"
}

variable "resource_group_name" {
  description = "Resource group the target VM is in. Used only to print a copy-pasteable boot-diagnostics command when the install hits its deadline; empty leaves a placeholder in that line."
  type        = string
  default     = ""
}

variable "kexec_image" {
  description = <<-DESC
    Override for the kexec installer tarball nixos-anywhere boots the target
    into, as a local path or a URL. null, the normal case, means the image
    this flake builds: the install runs `nix build` on kexec_flake_attr out
    of flake_path and hands over the tarball inside the result.

    No value asks for the upstream release asset (nixos-images nixos-25.05,
    Linux 6.14.10), the image every machine so far was installed by, whose
    MAC-matched DHCP units claim the accelerated-networking VF. That asset
    is the defect, so reaching it by omission is not something this module
    offers; someone who wants it can pass its URL, which shows up in a diff.

    Set this to pin an image while bisecting an install, or to a published
    URL. A local path is read on the machine running the apply and uploaded
    over the install SSH connection, which for a host means half a gigabyte
    through the edge's ProxyJump; a URL is handed to the target, which
    fetches it directly and never touches the jump host.
  DESC
  type        = string
  default     = null

  validation {
    # nixos-anywhere picks its tar decompression by matching the END of this
    # value (`\.tar\.gz$` and friends), so a URL with a SAS token or any
    # other query string appended would extract with no decompression and
    # fail on the target after the whole transfer. Caught here instead.
    condition     = var.kexec_image == null || can(regex("\\.tar(\\.gz|\\.xz|\\.zst)?$", var.kexec_image))
    error_message = "kexec_image must end in .tar.gz, .tar.xz, .tar.zst or .tar: nixos-anywhere reads the extension to know how to decompress it, and a query string after it makes the extraction fail on the target."
  }

  validation {
    # A hand-passed local path that is not there is the likely mistake,
    # because such a path is usually a nix store path and `nix store gc`
    # removes one nothing holds a root on. Checked before any VM exists
    # rather than after the apply has a machine to clean up.
    condition     = var.kexec_image == null || can(regex("^(https?|ftps?)://", var.kexec_image)) || fileexists(var.kexec_image)
    error_message = "kexec_image is a local path and there is no file there. Leave it null to let the install build the image, or rebuild it with `nix build '.?dir=nix#kexec-installer'` and pass the .tar.gz inside the result."
  }
}

variable "kexec_flake_attr" {
  description = <<-DESC
    `packages` attribute of flake_path that builds the kexec installer
    tarball, used whenever kexec_image is null. nix/flake.nix owns the
    attribute and nix/installer owns what is in the image; one image serves
    every machine, because what it exists to fix is the provider's NIC shape
    and not anything a particular host does.

    modules/environment builds this same attribute as a preflight before any
    VM exists, and passes the value down, so the two cannot name different
    attributes and build different images.
  DESC
  type        = string
  default     = "kexec-installer"
}

variable "install_deadline" {
  description = <<-DESC
    Wall-clock bound on the whole nixos-anywhere run, as a coreutils
    `timeout` duration. Not an SSH timeout: inside the run nixos-anywhere
    waits for the kexec with `until runSsh -o ConnectTimeout=10 -- exit 0;
    do sleep 5; done`, a loop with no bound and no flag to give it one, so
    a target that boots the installer and never gets a usable address is
    waited on forever. That is how one install held the production state
    lock for 16 hours 56 minutes, with every other apply -- the edge, the
    control plane, DNS, the vaults -- blocked behind it.

    OpenTofu cannot do this: local-exec takes no timeout argument
    ("An argument named \"timeout\" is not expected here"), and
    connect_timeout bounds only the SSH handshakes of the steps either
    side of the install. So the command is wrapped in `timeout`.

    The default is generous on purpose. A real install moves about 1.1 GB
    and takes 15 to 20 minutes, so 45 gives it better than twice that: the
    deadline exists to end a hang, not to race a slow but working install.
  DESC
  type        = string
  default     = "45m"

  validation {
    condition     = can(regex("^[0-9]+(\\.[0-9]+)?[smhd]?$", var.install_deadline))
    error_message = "install_deadline must be a coreutils timeout duration, e.g. 45m, 3600s or 1h. A value timeout cannot parse makes it exit 125 before nixos-anywhere starts."
  }
}
