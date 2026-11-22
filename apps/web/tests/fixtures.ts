// Spawns and tears down the two Go test fixtures (cmd/fakeapi, cmd/fake-
// logto) and the built dashboard server, for Playwright's global setup/
// teardown (08-dashboard.md §7: "Playwright end to end against
// internal/fakes/api ... with a fake Logto").
import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import net from 'node:net';
import { createInterface } from 'node:readline';
import path from 'node:path';

const REPO_ROOT = path.resolve(import.meta.dirname, '../../..');
const WEB_ROOT = path.resolve(import.meta.dirname, '..');

function resolveWebPort(): number {
	const fromEnv = Number(process.env.REPOSE_WEB_TEST_PORT);
	if (fromEnv > 0) return fromEnv;
	// Pick an unused ephemeral loopback port when REPOSE_WEB_TEST_PORT is
	// unset and export it into process.env so forked Playwright workers see
	// the exact same port without colliding with another worktree's server.
	const picked = Number(
		execFileSync(
			process.execPath,
			[
				'-e',
				'const s = require("node:net").createServer(); s.listen(0, "127.0.0.1", () => { process.stdout.write(String(s.address().port)); s.close(); });'
			],
			{ encoding: 'utf8' }
		).trim()
	);
	if (!picked) throw new Error('failed to allocate an ephemeral port for REPOSE_WEB_TEST_PORT');
	process.env.REPOSE_WEB_TEST_PORT = String(picked);
	return picked;
}

export const WEB_PORT = resolveWebPort();
export const BASE_URL = `http://127.0.0.1:${WEB_PORT}`;

const spawnedGroups = new Set<ChildProcessWithoutNullStreams>();
let cleanupRegistered = false;

function killGroup(child: ChildProcessWithoutNullStreams, signal: NodeJS.Signals): void {
	try {
		if (child.pid) {
			process.kill(-child.pid, signal);
			return;
		}
	} catch {
		// Group already exited; fall back to direct child signal below.
	}
	try {
		child.kill(signal);
	} catch {
		// Child already exited.
	}
}

function killAllSync(signal: NodeJS.Signals = 'SIGTERM'): void {
	for (const child of spawnedGroups) {
		killGroup(child, signal);
	}
	spawnedGroups.clear();
}

function registerProcessCleanup(): void {
	if (cleanupRegistered) return;
	cleanupRegistered = true;
	process.once('exit', () => killAllSync('SIGKILL'));
	for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP'] as const) {
		process.once(sig, () => {
			killAllSync('SIGTERM');
			process.exit(128 + (sig === 'SIGINT' ? 2 : sig === 'SIGTERM' ? 15 : 1));
		});
	}
}

function trackChild(child: ChildProcessWithoutNullStreams): ChildProcessWithoutNullStreams {
	registerProcessCleanup();
	spawnedGroups.add(child);
	child.once('exit', () => spawnedGroups.delete(child));
	return child;
}

export let fakeApiAdminURL = '';

/**
 * Ensures `127.0.0.1:port` is not already bound by another worktree's test
 * server. Never silently reuse an existing server on the port (ENG-10).
 */
async function assertPortFree(port: number): Promise<void> {
	await new Promise<void>((resolve, reject) => {
		const probe = net.createServer();
		probe.once('error', (err: NodeJS.ErrnoException) => {
			if (err.code === 'EADDRINUSE') {
				reject(
					new Error(
						`Port ${port} is already in use; refusing to connect to an existing server (set REPOSE_WEB_TEST_PORT to a free port or stop the process holding ${port})`
					)
				);
				return;
			}
			reject(err);
		});
		probe.listen({ port, host: '127.0.0.1', exclusive: true }, () => {
			probe.close((err) => (err ? reject(err) : resolve()));
		});
	});
}

/**
 * Reads a fixture's "KEY=value" stdout lines and resolves once every given
 * prefix has been seen, in one pass over the stream (a stream can only be
 * consumed once, so this reads all of a process's expected lines together
 * rather than opening a separate readline per prefix).
 */
function readLines(
	child: ChildProcessWithoutNullStreams,
	prefixes: string[]
): Promise<Record<string, string>> {
	return new Promise((resolve, reject) => {
		const found: Record<string, string> = {};
		const rl = createInterface({ input: child.stdout });
		const timeout = setTimeout(
			() => reject(new Error(`${prefixes.join(',')}: timed out waiting for stdout`)),
			15_000
		);
		rl.on('line', (line) => {
			for (const prefix of prefixes) {
				if (line.startsWith(prefix)) found[prefix] = line.slice(prefix.length).trim();
			}
			if (prefixes.every((p) => p in found)) {
				clearTimeout(timeout);
				rl.close();
				resolve(found);
			}
		});
		child.once('exit', (code) => {
			clearTimeout(timeout);
			rl.close();
			reject(new Error(`${prefixes.join(',')}: process exited early with code ${code}`));
		});
	});
}

async function waitForHealthz(
	url: string,
	child: ChildProcessWithoutNullStreams,
	timeoutMs = 20_000
): Promise<void> {
	let exited = false;
	let exitCode: number | null = null;
	const onExit = (code: number | null) => {
		exited = true;
		exitCode = code;
	};
	child.once('exit', onExit);
	try {
		const deadline = Date.now() + timeoutMs;
		for (;;) {
			if (exited) {
				throw new Error(`web server exited early with code ${exitCode} while waiting for ${url}`);
			}
			try {
				const res = await fetch(url);
				if (res.ok) return;
			} catch {
				// not up yet
			}
			if (Date.now() > deadline) throw new Error(`timed out waiting for ${url}`);
			await new Promise((r) => setTimeout(r, 100));
		}
	} finally {
		child.off('exit', onExit);
	}
}

export async function startAll(): Promise<void> {
	await assertPortFree(WEB_PORT);

	try {
		// `go run` builds a binary and runs it as its own child, and a SIGTERM to
		// `go run` does not reach that child: the fixtures outlived every suite.
		// Each fixture is started as the leader of a process group of its own
		// (detached), so stopAll and signal handlers can terminate the whole group.
		const fakeapi = trackChild(
			spawn('go', ['run', './cmd/fakeapi'], { cwd: REPO_ROOT, detached: true })
		);
		const fakeapiLines = await readLines(fakeapi, ['FAKEAPI_URL=', 'FAKEAPI_ADMIN_URL=']);
		const fakeApiURL = fakeapiLines['FAKEAPI_URL='];
		fakeApiAdminURL = fakeapiLines['FAKEAPI_ADMIN_URL='];

		const fakeLogto = trackChild(
			spawn('go', ['run', './cmd/fake-logto'], {
				cwd: REPO_ROOT,
				detached: true
			})
		);
		const fakeLogtoURL = (await readLines(fakeLogto, ['FAKELOGTO_URL=']))['FAKELOGTO_URL='];

		// Test files run in worker processes forked after globalSetup returns,
		// which inherit process.env as it stands at fork time — this is how a
		// spec file finds the fixtures' (randomly chosen) ports.
		process.env.PUBLIC_API_URL = fakeApiURL;
		process.env.PUBLIC_LOGTO_ENDPOINT = fakeLogtoURL;
		process.env.FAKEAPI_ADMIN_URL = fakeApiAdminURL;

		const web = trackChild(
			spawn('node', ['build'], {
				cwd: WEB_ROOT,
				detached: true,
				env: {
					...process.env,
					PORT: String(WEB_PORT),
					HOST: '127.0.0.1',
					// adapter-node reads https off a request it is not told about,
					// which is right for the deployed site behind Traefik and wrong
					// for a fixture on plain http loopback. /signin builds its OIDC
					// redirect_uri from this origin (DECISIONS I-878), so without it
					// the fake sends the browser to https://127.0.0.1:<port>/callback.
					ORIGIN: BASE_URL,
					PUBLIC_API_URL: fakeApiURL,
					PUBLIC_LOGTO_ENDPOINT: fakeLogtoURL,
					PUBLIC_LOGTO_APP_ID: 'dashboard-test'
				}
			})
		);
		web.stderr.on('data', (d) => process.stderr.write(`[web] ${d}`));

		await waitForHealthz(`${BASE_URL}/healthz`, web);
	} catch (err) {
		await stopAll();
		throw err;
	}
}

export async function stopAll(): Promise<void> {
	killAllSync('SIGTERM');
}
