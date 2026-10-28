// Spawns and tears down the two Go test fixtures (cmd/fakeapi, cmd/fake-
// logto) and the built dashboard server, for Playwright's global setup/
// teardown (08-dashboard.md §7: "Playwright end to end against
// internal/fakes/api ... with a fake Logto").
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { createInterface } from 'node:readline';
import path from 'node:path';

const REPO_ROOT = path.resolve(import.meta.dirname, '../../..');
const WEB_ROOT = path.resolve(import.meta.dirname, '..');
// REPOSE_WEB_TEST_PORT lets a second worktree's suite run beside one on 4173.
export const WEB_PORT = Number(process.env.REPOSE_WEB_TEST_PORT) || 4173;
export const BASE_URL = `http://127.0.0.1:${WEB_PORT}`;

interface Running {
	fakeapi: ChildProcessWithoutNullStreams;
	fakeLogto: ChildProcessWithoutNullStreams;
	web: ChildProcessWithoutNullStreams;
}

let running: Running | undefined;
export let fakeApiAdminURL = '';

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
		child.once('exit', (code) =>
			reject(new Error(`${prefixes.join(',')}: process exited early with code ${code}`))
		);
	});
}

async function waitForHealthz(url: string, timeoutMs = 20_000): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		try {
			const res = await fetch(url);
			if (res.ok) return;
		} catch {
			// not up yet
		}
		if (Date.now() > deadline) throw new Error(`timed out waiting for ${url}`);
		await new Promise((r) => setTimeout(r, 200));
	}
}

export async function startAll(): Promise<void> {
	// `go run` builds a binary and runs it as its own child, and a SIGTERM to
	// `go run` does not reach that child: the fixtures outlived every suite.
	// Each fixture is started as the leader of a process group of its own
	// (detached), so stopAll can signal the whole group.
	const fakeapi = spawn('go', ['run', './cmd/fakeapi'], { cwd: REPO_ROOT, detached: true });
	const fakeapiLines = await readLines(fakeapi, ['FAKEAPI_URL=', 'FAKEAPI_ADMIN_URL=']);
	const fakeApiURL = fakeapiLines['FAKEAPI_URL='];
	fakeApiAdminURL = fakeapiLines['FAKEAPI_ADMIN_URL='];

	const fakeLogto = spawn('go', ['run', './cmd/fake-logto'], {
		cwd: REPO_ROOT,
		detached: true
	});
	const fakeLogtoURL = (await readLines(fakeLogto, ['FAKELOGTO_URL=']))['FAKELOGTO_URL='];

	// Test files run in worker processes forked after globalSetup returns,
	// which inherit process.env as it stands at fork time — this is how a
	// spec file finds the fixtures' (randomly chosen) ports.
	process.env.PUBLIC_API_URL = fakeApiURL;
	process.env.PUBLIC_LOGTO_ENDPOINT = fakeLogtoURL;
	process.env.FAKEAPI_ADMIN_URL = fakeApiAdminURL;

	const web = spawn('node', ['build'], {
		cwd: WEB_ROOT,
		env: {
			...process.env,
			PORT: String(WEB_PORT),
			HOST: '127.0.0.1',
			// adapter-node reads https off a request it is not told about,
			// which is right for the deployed site behind Traefik and wrong
			// for a fixture on plain http loopback. /signin builds its OIDC
			// redirect_uri from this origin (DECISIONS I-878), so without it
			// the fake sends the browser to https://127.0.0.1:4173/callback.
			ORIGIN: BASE_URL,
			PUBLIC_API_URL: fakeApiURL,
			PUBLIC_LOGTO_ENDPOINT: fakeLogtoURL,
			PUBLIC_LOGTO_APP_ID: 'dashboard-test'
		}
	});
	web.stderr.on('data', (d) => process.stderr.write(`[web] ${d}`));

	await waitForHealthz(`${BASE_URL}/healthz`);

	running = { fakeapi, fakeLogto, web };
}

export async function stopAll(): Promise<void> {
	if (!running) return;
	running.web.kill('SIGTERM');
	for (const child of [running.fakeLogto, running.fakeapi]) {
		// The negative pid names the group: `go run` and the binary it built.
		try {
			if (child.pid) process.kill(-child.pid, 'SIGTERM');
		} catch {
			// The group has already exited.
		}
	}
	running = undefined;
}
