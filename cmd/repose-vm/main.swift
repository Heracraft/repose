// repose-vm: one Virtualization.framework VM a process, for hostd on macOS
// (design 2, DECISIONS I-1664).
//
// It takes the Cloud Hypervisor argv hostd already renders (ch.Spec.Args,
// ch.RestoreArgs) and answers the parts of Cloud Hypervisor hostd talks to,
// so hostd's ch.HTTP client, vsockclient.CHDialer and console tailer work
// unchanged:
//
//   --api-socket PATH   CH's HTTP API subset: PUT vm.shutdown, vmm.shutdown,
//                       vm.pause, vm.resume, vm.snapshot; GET vm.info,
//                       vmm.ping; vm.resize-disk is refused (VZ cannot
//                       resize an attached disk; the guest grows at its next
//                       start, I-1660).
//   --vsock cid=N,socket=PATH   CH's hybrid vsock: a client connects, sends
//                       "CONNECT <port>\n", gets "OK <port>\n" and a stream
//                       to that guest port.
//   --serial socket=PATH  the guest's console (hvc0) on a unix socket.
//   --kernel --initramfs --cmdline --cpus boot=N --memory size=NM
//   --disk path=P[,readonly=on]... (in order: vda, vdb)
//   --net tap=T,mac=M   a file-handle NIC: unixgram to NETDIR/T.sock, the
//                       first datagram "VFKT" (vfkit's protocol), then one
//                       Ethernet frame a datagram. Without --net-dir it is
//                       VZ's NAT (lab only).
//   --balloon ...       VZ's traditional balloon (no free page reporting).
//   --restore source_url=file://DIR  restore DIR/vz.state with the config
//                       saved beside it (DIR/repose-vm.json); the VM comes up
//                       paused, as CH's does, and the API socket appears once
//                       it is restored.
//   --console off, --seccomp, --fs: accepted, ignored (no virtiofsd here).
//
// Its own flags, which hostd's launchd substrate appends:
//   --net-dir DIR --net-mtu N   the switch's sockets and the NIC's MTU
//   --park-dir DIR      on SIGTERM a running VM is paused and saved to DIR
//                       before the process exits (a launcher upgrade, a
//                       park repose starts); without it SIGTERM stops the VM.
//
// The save file is encrypted by VZ with a Secure Enclave key of the logged-in
// user: it must run as that user, in that user's session, and a restore
// needs the session unlocked (DECISIONS I-1662). It is signed ad hoc with
// com.apple.security.virtualization (build.sh).
import Darwin
import Foundation
import Virtualization

setvbuf(stdout, nil, _IOLBF, 0)
setvbuf(stderr, nil, _IOLBF, 0)

func now() -> Double { Date().timeIntervalSince1970 }
// say writes one event line. Ids, states and durations only; never what a
// guest printed (that goes to the console socket).
func say(_ s: String) { FileHandle.standardError.write((String(format: "%.3f repose-vm ", now()) + s + "\n").data(using: .utf8)!) }
func die(_ s: String, _ code: Int32 = 2) -> Never { say("fatal " + s); exit(code) }

// ---------------------------------------------------------------- argv ---

struct Disk: Codable { var path: String; var readOnly: Bool }

struct Spec: Codable {
  var apiSocket = ""
  var kernel = ""
  var initramfs = ""
  var cmdline = ""
  var cpus = 1
  var memMiB: UInt64 = 1024
  var disks: [Disk] = []
  var tap = ""
  var mac = ""
  var vsockSocket = ""
  var serialSocket = ""
  var balloon = false
  var netDir = ""
  var netMTU = 1500
  var parkDir = ""
}

// opts parses CH's "k=v,k=v" option lists.
func opts(_ s: String) -> [String: String] {
  var m: [String: String] = [:]
  for kv in s.split(separator: ",") {
    let p = kv.split(separator: "=", maxSplits: 1).map(String.init)
    m[p[0]] = p.count > 1 ? p[1] : ""
  }
  return m
}

// mib parses CH's sizes: 4096M, 4G, 1048576K, plain bytes.
func mib(_ s: String) -> UInt64 {
  let u = s.uppercased()
  if u.hasSuffix("G") { return UInt64(u.dropLast())! * 1024 }
  if u.hasSuffix("M") { return UInt64(u.dropLast())! }
  if u.hasSuffix("K") { return UInt64(u.dropLast())! / 1024 }
  return UInt64(u)! / (1024 * 1024)
}

// fileURLPath reads a source_url=file:///dir.
func fileURLPath(_ s: String) -> String {
  guard let u = URL(string: s), u.isFileURL else { die("restore source_url \(s) is not a file URL") }
  return u.path
}

var spec = Spec()
var restoreDir: String? = nil
var bootArgs = Array(CommandLine.arguments.dropFirst())

func parse(_ a: [String], into s: inout Spec, restore: inout String?) {
  var i = 0
  func next() -> String {
    i += 1
    guard i < a.count else { die("\(a[i - 1]) needs a value") }
    return a[i]
  }
  while i < a.count {
    let f = a[i]
    switch f {
    case "--api-socket": s.apiSocket = next()
    case "--kernel": s.kernel = next()
    case "--initramfs": s.initramfs = next()
    case "--cmdline": s.cmdline = next()
    case "--cpus": s.cpus = Int(opts(next())["boot"] ?? "1") ?? 1
    case "--memory": s.memMiB = mib(opts(next())["size"] ?? "1024M")
    case "--disk":
      let o = opts(next())
      guard let p = o["path"] else { die("--disk without path") }
      s.disks.append(Disk(path: p, readOnly: o["readonly"] == "on"))
    case "--net":
      let o = opts(next()); s.tap = o["tap"] ?? ""; s.mac = o["mac"] ?? ""
    case "--vsock": s.vsockSocket = opts(next())["socket"] ?? ""
    case "--serial": s.serialSocket = opts(next())["socket"] ?? ""
    case "--balloon": _ = next(); s.balloon = true
    case "--restore": restore = fileURLPath(opts(next())["source_url"] ?? "")
    case "--console", "--seccomp", "--fs": _ = next()
    case "--net-dir": s.netDir = next()
    case "--net-mtu": s.netMTU = Int(next()) ?? 1500
    case "--park-dir": s.parkDir = next()
    case "cloud-hypervisor": break  // argv[0] as hostd renders it, when passed through
    default: die("unknown argument \(f)")
    }
    i += 1
  }
}

parse(bootArgs, into: &spec, restore: &restoreDir)

// A restore takes the boot's config from the snapshot (as CH's config.json
// does): the disks, kernel, NIC and sockets are the guest's of today, all
// derived from its id. The substrate's own flags of this start win.
struct Saved: Codable { var args: [String]; var machineID: Data }
var savedMachineID: Data? = nil
if let dir = restoreDir {
  guard let d = FileManager.default.contents(atPath: dir + "/repose-vm.json"),
        let sv = try? JSONDecoder().decode(Saved.self, from: d) else { die("no repose-vm.json in the restore directory", 3) }
  let api = spec.apiSocket, keep = spec
  var s2 = Spec(), r2: String? = nil
  parse(sv.args, into: &s2, restore: &r2)
  s2.apiSocket = api.isEmpty ? s2.apiSocket : api
  if !keep.netDir.isEmpty { s2.netDir = keep.netDir }
  if keep.netMTU != 1500 { s2.netMTU = keep.netMTU }
  s2.parkDir = keep.parkDir
  spec = s2
  bootArgs = sv.args
  savedMachineID = sv.machineID
}
// The boot argv without the substrate's flags, which a later save stores.
func coreArgs(_ a: [String]) -> [String] {
  var out: [String] = []; var i = 0
  while i < a.count {
    if ["--net-dir", "--net-mtu", "--park-dir"].contains(a[i]) { i += 2; continue }
    out.append(a[i]); i += 1
  }
  return out
}
bootArgs = coreArgs(bootArgs)

guard !spec.apiSocket.isEmpty else { die("--api-socket is required") }
let guestDir = (spec.apiSocket as NSString).deletingLastPathComponent

// The kernel line hostd renders names the serial console CH gives aarch64
// (ttyAMA0, a PL011); VZ's is a virtio console, hvc0.
func vzCmdline(_ c: String) -> String {
  c.split(separator: " ").map { w -> String in
    w.hasPrefix("console=") ? "console=hvc0" : String(w)
  }.joined(separator: " ")
}

// ---------------------------------------------------------------- sockets ---

func sockaddrUn(_ p: String) -> sockaddr_un {
  var sa = sockaddr_un()
  sa.sun_family = sa_family_t(AF_UNIX)
  let u = Array(p.utf8)
  // macOS's sun_path is 104 bytes with its NUL.
  if u.count >= MemoryLayout.size(ofValue: sa.sun_path) { die("unix socket path of \(u.count) bytes is too long (under 104): \(p)") }
  withUnsafeMutableBytes(of: &sa.sun_path) { b in for i in 0..<u.count { b[i] = u[i] } }
  return sa
}

func listenUnix(_ path: String) -> Int32 {
  unlink(path)
  let fd = socket(AF_UNIX, SOCK_STREAM, 0)
  precondition(fd >= 0)
  var sa = sockaddrUn(path)
  let len = socklen_t(MemoryLayout<sockaddr_un>.size)
  let rc = withUnsafePointer(to: &sa) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, len) } }
  if rc != 0 { die("bind \(path): errno \(errno)") }
  chmod(path, 0o600)
  if listen(fd, 128) != 0 { die("listen \(path): errno \(errno)") }
  return fd
}

func writeAll(_ fd: Int32, _ d: Data) -> Bool {
  var ok = true
  d.withUnsafeBytes { (b: UnsafeRawBufferPointer) in
    var off = 0
    while off < b.count {
      let n = Darwin.write(fd, b.baseAddress! + off, b.count - off)
      if n < 0 { if errno == EINTR { continue }; ok = false; return }
      off += n
    }
  }
  return ok
}

// acceptLoop runs handle for each connection on its own thread.
func acceptLoop(_ lfd: Int32, _ name: String, _ handle: @escaping (Int32) -> Void) {
  Thread.detachNewThread {
    while true {
      let c = accept(lfd, nil, nil)
      if c < 0 { if errno == EINTR || errno == ECONNABORTED { continue }; say("accept \(name) errno \(errno)"); return }
      var one: Int32 = 1
      setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(4))
      Thread.detachNewThread { handle(c) }
    }
  }
}

// pump copies from one fd to another until EOF, then half-closes.
func pump(_ from: Int32, _ to: Int32, onEnd: @escaping () -> Void) {
  Thread.detachNewThread {
    var buf = [UInt8](repeating: 0, count: 256 * 1024)
    while true {
      let n = buf.withUnsafeMutableBytes { read(from, $0.baseAddress!, $0.count) }
      if n < 0 && errno == EINTR { continue }
      if n <= 0 { break }
      if !writeAll(to, Data(buf[0..<n])) { break }
    }
    shutdown(to, SHUT_WR)
    onEnd()
  }
}

// ---------------------------------------------------------------- config ---

func machineIDPath() -> String { guestDir + "/vz.machine-id" }

func platform() -> VZGenericPlatformConfiguration {
  let p = VZGenericPlatformConfiguration()
  // A restore needs the identifier the VM was saved with (tart 3207aa6).
  if let d = savedMachineID ?? FileManager.default.contents(atPath: machineIDPath()),
     let id = VZGenericMachineIdentifier(dataRepresentation: d) {
    p.machineIdentifier = id
  }
  FileManager.default.createFile(atPath: machineIDPath(), contents: p.machineIdentifier.dataRepresentation,
                                 attributes: [.posixPermissions: 0o600])
  return p
}

// The NIC's datagram socket and the switch port it is connected to. A new
// hostd binds a new port socket: the launcher connects to it again when
// told (vm.net-reconnect) or when it sees the socket file change.
var netFd: Int32 = -1
var netRemote = ""
var netInode: ino_t = 0
let netLock = NSLock()

func inode(_ p: String) -> ino_t {
  var st = stat()
  return stat(p, &st) == 0 ? st.st_ino : 0
}

// netConnect (re)connects netFd to netRemote and sends vfkit's "VFKT".
func netConnect() -> Bool {
  netLock.lock(); defer { netLock.unlock() }
  var r = sockaddrUn(netRemote)
  let len = socklen_t(MemoryLayout<sockaddr_un>.size)
  if withUnsafePointer(to: &r, { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(netFd, $0, len) } }) != 0 {
    say("event=net_connect_error errno=\(errno)")
    return false
  }
  netInode = inode(netRemote)
  _ = "VFKT".withCString { send(netFd, $0, 4, 0) }
  return true
}

func unixgram(_ remote: String, local: String) -> Int32 {
  let fd = socket(AF_UNIX, SOCK_DGRAM, 0)
  precondition(fd >= 0)
  unlink(local)
  var l = sockaddrUn(local)
  let len = socklen_t(MemoryLayout<sockaddr_un>.size)
  if withUnsafePointer(to: &l, { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, len) } }) != 0 { die("bind \(local): errno \(errno)") }
  var snd: Int32 = 1 << 20, rcv: Int32 = 4 << 20
  setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &snd, socklen_t(4))
  setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &rcv, socklen_t(4))
  netFd = fd; netRemote = remote
  if !netConnect() { die("connect \(remote): the switch port is not there", 4) }
  // Watch the port: a hostd that restarted binds a new socket file.
  Thread.detachNewThread {
    while true {
      sleep(1)
      let i = inode(netRemote)
      netLock.lock(); let changed = i != 0 && i != netInode; netLock.unlock()
      if changed && netConnect() { say("event=net_reconnected by=watch") }
    }
  }
  return fd
}

// The console: VZ writes the guest's hvc0 to a pipe this process drains
// always (a full pipe would stall the guest) and copies to whoever is
// connected to the serial socket; what they type goes to the guest.
let consoleOut = Pipe(), consoleIn = Pipe()
let consoleLock = NSLock()
var consoleClients: [Int32] = []

func configuration() -> VZVirtualMachineConfiguration {
  let c = VZVirtualMachineConfiguration()
  let bl = VZLinuxBootLoader(kernelURL: URL(fileURLWithPath: spec.kernel))
  if !spec.initramfs.isEmpty { bl.initialRamdiskURL = URL(fileURLWithPath: spec.initramfs) }
  bl.commandLine = vzCmdline(spec.cmdline)
  c.bootLoader = bl
  c.cpuCount = max(VZVirtualMachineConfiguration.minimumAllowedCPUCount, min(spec.cpus, VZVirtualMachineConfiguration.maximumAllowedCPUCount))
  c.memorySize = spec.memMiB * 1024 * 1024
  c.platform = platform()
  do {
    c.storageDevices = try spec.disks.map { d in
      let a = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: d.path), readOnly: d.readOnly,
                                                     cachingMode: .automatic, synchronizationMode: .full)
      return VZVirtioBlockDeviceConfiguration(attachment: a)
    }
  } catch { die("disk: \(error)") }
  let n = VZVirtioNetworkDeviceConfiguration()
  if spec.netDir.isEmpty {
    n.attachment = VZNATNetworkDeviceAttachment()
  } else {
    let fd = unixgram(spec.netDir + "/" + spec.tap + ".sock", local: guestDir + "/net0.dgram")
    let at = VZFileHandleNetworkDeviceAttachment(fileHandle: FileHandle(fileDescriptor: fd, closeOnDealloc: true))
    at.maximumTransmissionUnit = spec.netMTU
    n.attachment = at
  }
  if !spec.mac.isEmpty, let m = VZMACAddress(string: spec.mac) { n.macAddress = m }
  c.networkDevices = [n]
  c.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
  if spec.balloon { c.memoryBalloonDevices = [VZVirtioTraditionalMemoryBalloonDeviceConfiguration()] }
  c.socketDevices = [VZVirtioSocketDeviceConfiguration()]
  let con = VZVirtioConsoleDeviceSerialPortConfiguration()
  con.attachment = VZFileHandleSerialPortAttachment(fileHandleForReading: consoleIn.fileHandleForReading,
                                                    fileHandleForWriting: consoleOut.fileHandleForWriting)
  c.serialPorts = [con]
  do { try c.validate() } catch { die("config: \(error)") }
  return c
}

let vmConfig = configuration()
let vm = VZVirtualMachine(configuration: vmConfig)

final class Delegate: NSObject, VZVirtualMachineDelegate {
  func guestDidStop(_ vm: VZVirtualMachine) { say("event=guest_stopped"); exit(0) }
  func virtualMachine(_ vm: VZVirtualMachine, didStopWithError e: Error) { say("event=vm_error err=\((e as NSError).code)"); exit(1) }
  func virtualMachine(_ vm: VZVirtualMachine, networkDevice: VZNetworkDevice, attachmentWasDisconnectedWithError e: Error) {
    say("event=nic_disconnected err=\((e as NSError).code)")
  }
}
let delegate = Delegate()
vm.delegate = delegate

func stateName() -> String {
  switch vm.state {
  case .running: return "Running"
  case .paused: return "Paused"
  case .stopped: return "Shutdown"
  case .starting, .resuming, .restoring: return "Created"
  case .pausing, .saving: return "Running"
  case .stopping: return "Shutdown"
  case .error: return "Shutdown"
  @unknown default: return ""
  }
}

// onMain runs f on the VM's queue (main) and waits for its completion.
func onMain<T>(_ f: @escaping (@escaping (T) -> Void) -> Void) -> T {
  let sem = DispatchSemaphore(value: 0)
  var out: T? = nil
  DispatchQueue.main.async { f { v in out = v; sem.signal() } }
  sem.wait()
  return out!
}

// save writes the paused VM to dir: VZ's state file and the argv and
// machine identifier a restore needs.
func save(_ dir: String, _ done: @escaping (String?) -> Void) {
  do { try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700]) }
  catch { done("mkdir: \(error)"); return }
  let t = now()
  let id = (vmConfig.platform as? VZGenericPlatformConfiguration)?.machineIdentifier.dataRepresentation ?? Data()
  do {
    let j = try JSONEncoder().encode(Saved(args: bootArgs, machineID: id))
    try j.write(to: URL(fileURLWithPath: dir + "/repose-vm.json"))
  } catch { done("write config: \(error)"); return }
  vm.saveMachineStateTo(url: URL(fileURLWithPath: dir + "/vz.state")) { e in
    if let e = e { say("event=save_error code=\((e as NSError).code)"); done("save: \(e.localizedDescription)"); return }
    let sz = (try? FileManager.default.attributesOfItem(atPath: dir + "/vz.state")[.size] as? UInt64) ?? 0
    say(String(format: "event=saved s=%.3f bytes=%llu", now() - t, sz))
    done(nil)
  }
}

// ---------------------------------------------------------------- API ---

struct Req { var method: String; var path: String; var body: Data }

func readRequest(_ fd: Int32) -> Req? {
  var buf = Data()
  var tmp = [UInt8](repeating: 0, count: 65536)
  var headerEnd: Range<Data.Index>? = nil
  while headerEnd == nil {
    let n = tmp.withUnsafeMutableBytes { read(fd, $0.baseAddress!, $0.count) }
    if n <= 0 { return nil }
    buf.append(contentsOf: tmp[0..<n])
    headerEnd = buf.range(of: Data("\r\n\r\n".utf8))
    if buf.count > 1 << 20 { return nil }
  }
  let head = String(decoding: buf[..<headerEnd!.lowerBound], as: UTF8.self)
  let lines = head.components(separatedBy: "\r\n")
  let first = lines[0].split(separator: " ").map(String.init)
  guard first.count >= 2 else { return nil }
  var clen = 0
  for l in lines.dropFirst() {
    let kv = l.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
    if kv.count == 2 && kv[0].lowercased() == "content-length" { clen = Int(kv[1]) ?? 0 }
  }
  var body = Data(buf[headerEnd!.upperBound...])
  while body.count < clen {
    let n = tmp.withUnsafeMutableBytes { read(fd, $0.baseAddress!, $0.count) }
    if n <= 0 { break }
    body.append(contentsOf: tmp[0..<n])
  }
  return Req(method: first[0], path: first[1], body: body)
}

func respond(_ fd: Int32, _ code: Int, _ body: String = "") {
  let reason = [200: "OK", 204: "No Content", 400: "Bad Request", 404: "Not Found", 405: "Method Not Allowed", 500: "Internal Server Error"][code] ?? "Error"
  var s = "HTTP/1.1 \(code) \(reason)\r\nConnection: close\r\nContent-Length: \(body.utf8.count)\r\n"
  if !body.isEmpty { s += "Content-Type: application/json\r\n" }
  s += "\r\n" + body
  _ = writeAll(fd, Data(s.utf8))
}

func jsonString(_ s: String) -> String {
  String(data: try! JSONSerialization.data(withJSONObject: [s], options: []), encoding: .utf8)!.dropFirst().dropLast().description
}

func info() -> String {
  let st: String = onMain { k in k(stateName()) }
  return "{\"state\":\(jsonString(st)),\"config\":{\"cpus\":{\"boot_vcpus\":\(spec.cpus)},\"memory\":{\"size\":\(spec.memMiB * 1024 * 1024)}},\"runtime\":\"vz\"}"
}

func handleAPI(_ fd: Int32) {
  defer { close(fd) }
  guard let r = readRequest(fd) else { return }
  let verb = r.path.hasPrefix("/api/v1/") ? String(r.path.dropFirst(8)) : r.path
  switch (r.method, verb) {
  case ("GET", "vm.info"): respond(fd, 200, info())
  case ("GET", "vmm.ping"): respond(fd, 200, "{\"version\":\"repose-vm\"}")
  case ("PUT", "vm.pause"):
    let e: String? = onMain { k in
      guard vm.canPause else { k("cannot pause in state \(stateName())"); return }
      vm.pause { r in if case .failure(let e) = r { k(e.localizedDescription) } else { k(nil) } }
    }
    if let e = e { respond(fd, 500, "{\"error\":\(jsonString(e))}") } else { say("event=paused"); respond(fd, 204) }
  case ("PUT", "vm.resume"):
    let e: String? = onMain { k in
      guard vm.canResume else { k("cannot resume in state \(stateName())"); return }
      vm.resume { r in if case .failure(let e) = r { k(e.localizedDescription) } else { k(nil) } }
    }
    if let e = e { respond(fd, 500, "{\"error\":\(jsonString(e))}") } else { say("event=resumed"); respond(fd, 204) }
  case ("PUT", "vm.shutdown"):
    let e: String? = onMain { k in
      guard vm.canStop else { k(nil); return }
      vm.stop { e in k(e?.localizedDescription) }
    }
    if let e = e { respond(fd, 500, "{\"error\":\(jsonString(e))}") } else { say("event=shutdown"); respond(fd, 204) }
  case ("PUT", "vmm.shutdown"):
    respond(fd, 204)
    say("event=vmm_shutdown")
    exit(0)
  case ("PUT", "vm.snapshot"):
    guard let o = try? JSONSerialization.jsonObject(with: r.body) as? [String: Any],
          let u = o["destination_url"] as? String, let url = URL(string: u), url.isFileURL else {
      respond(fd, 400, "{\"error\":\"destination_url must be a file URL\"}"); return
    }
    let e: String? = onMain { k in
      guard vm.state == .paused else { k("the VM must be paused to snapshot, it is \(stateName())"); return }
      save(url.path, k)
    }
    if let e = e { respond(fd, 500, "{\"error\":\(jsonString(e))}") } else { respond(fd, 204) }
  case ("PUT", "vm.net-reconnect"):
    if netFd < 0 { respond(fd, 500, "{\"error\":\"this VM has no switch port\"}"); return }
    if netConnect() { say("event=net_reconnected by=api"); respond(fd, 204) } else { respond(fd, 500, "{\"error\":\"connect to the switch port failed\"}") }
  case ("PUT", "vm.resize-disk"):
    respond(fd, 500, "{\"error\":\"resize of a running VM's disk is not supported on this host\"}")
  default:
    respond(fd, 404, "{\"error\":\"not supported\"}")
  }
}

// ---------------------------------------------------------------- vsock ---

func handleVsock(_ fd: Int32) {
  var line = Data()
  var b: UInt8 = 0
  while line.count < 64 {
    if read(fd, &b, 1) != 1 { close(fd); return }
    if b == 10 { break }
    line.append(b)
  }
  let w = String(decoding: line, as: UTF8.self).split(separator: " ")
  guard w.count == 2, w[0] == "CONNECT", let port = UInt32(w[1].trimmingCharacters(in: .whitespacesAndNewlines)) else { close(fd); return }
  let conn: VZVirtioSocketConnection? = onMain { k in
    guard vm.state == .running, let dev = vm.socketDevices.first as? VZVirtioSocketDevice else { k(nil); return }
    dev.connect(toPort: port) { r in
      switch r {
      case .success(let c): k(c)
      case .failure: k(nil)
      }
    }
  }
  guard let c = conn else { close(fd); return }
  _ = writeAll(fd, Data("OK \(port)\n".utf8))
  let gfd = c.fileDescriptor
  let lock = NSLock(); var ended = 0
  let end = {
    lock.lock(); ended += 1; let both = ended == 2; lock.unlock()
    if both { close(fd); withExtendedLifetime(c) { c.close() } }
  }
  pump(fd, gfd, onEnd: end)
  pump(gfd, fd, onEnd: end)
}

// ---------------------------------------------------------------- console ---

func startConsole() {
  let out = consoleOut.fileHandleForReading.fileDescriptor
  Thread.detachNewThread {
    var buf = [UInt8](repeating: 0, count: 16384)
    while true {
      let n = buf.withUnsafeMutableBytes { read(out, $0.baseAddress!, $0.count) }
      if n < 0 && errno == EINTR { continue }
      if n <= 0 { return }
      let d = Data(buf[0..<n])
      consoleLock.lock()
      consoleClients = consoleClients.filter { c in
        if writeAll(c, d) { return true }
        close(c)
        return false
      }
      consoleLock.unlock()
    }
  }
  guard !spec.serialSocket.isEmpty else { return }
  let l = listenUnix(spec.serialSocket)
  acceptLoop(l, "serial") { c in
    consoleLock.lock(); consoleClients.append(c); consoleLock.unlock()
    var buf = [UInt8](repeating: 0, count: 4096)
    let inFd = consoleIn.fileHandleForWriting.fileDescriptor
    while true {
      let n = buf.withUnsafeMutableBytes { read(c, $0.baseAddress!, $0.count) }
      if n < 0 && errno == EINTR { continue }
      if n <= 0 { break }
      _ = writeAll(inFd, Data(buf[0..<n]))
    }
  }
}

func startSockets() {
  if !spec.vsockSocket.isEmpty { acceptLoop(listenUnix(spec.vsockSocket), "vsock", handleVsock) }
  acceptLoop(listenUnix(spec.apiSocket), "api", handleAPI)
}

// ---------------------------------------------------------------- start ---

signal(SIGPIPE, SIG_IGN)
signal(SIGTERM, SIG_IGN)
let term = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
term.setEventHandler {
  let t = now()
  say("event=sigterm state=\(stateName())")
  if !spec.parkDir.isEmpty && (vm.state == .running || vm.state == .paused) {
    let go = { save(spec.parkDir) { e in
      if let e = e { say("event=park_failed reason=\(jsonString(e))"); exit(1) }
      say(String(format: "event=parked s=%.3f", now() - t)); exit(0) } }
    if vm.state == .paused { go() } else { vm.pause { _ in go() } }
    return
  }
  if vm.canStop { vm.stop { _ in exit(0) } } else { exit(0) }
}
term.resume()

startConsole()
let t0 = now()
say("event=launch pid=\(getpid()) restore=\(restoreDir != nil) cpus=\(vmConfig.cpuCount) mem_mib=\(spec.memMiB) net=\(spec.netDir.isEmpty ? "nat" : "switch")")
if let dir = restoreDir {
  vm.restoreMachineStateFrom(url: URL(fileURLWithPath: dir + "/vz.state")) { e in
    if let e = e { say("event=restore_error code=\((e as NSError).code) msg=\(jsonString(e.localizedDescription))"); exit(3) }
    say(String(format: "event=restored s=%.3f", now() - t0))
    // CH comes up paused after --restore and its API socket answers from
    // then on; hostd resumes it.
    startSockets()
  }
} else {
  startSockets()
  vm.start { r in
    switch r {
    case .success: say(String(format: "event=started s=%.3f", now() - t0))
    case .failure(let e): say("event=start_error code=\((e as NSError).code) msg=\(jsonString(e.localizedDescription))"); exit(1)
    }
  }
}
RunLoop.main.run()
