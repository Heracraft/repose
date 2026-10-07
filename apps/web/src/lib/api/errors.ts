import type { ApiErrorBody, ErrorCode } from './types';

/** An error envelope the api returned, per docs/interfaces/api.md. */
export class ApiError extends Error {
	code: ErrorCode;
	detail?: Record<string, unknown>;
	requestId: string | null;
	status: number;

	constructor(body: ApiErrorBody, status: number, requestId: string | null) {
		super(body.message);
		this.name = 'ApiError';
		this.code = body.code;
		this.detail = body.detail;
		this.requestId = requestId;
		this.status = status;
	}
}

/** The api could not be reached at all: DNS, TLS, connection refused, offline. */
export class NetworkError extends Error {
	constructor(cause: unknown) {
		super('Cannot reach the API');
		this.name = 'NetworkError';
		this.cause = cause;
	}
}

export function isApiError(e: unknown, code?: ErrorCode): e is ApiError {
	return e instanceof ApiError && (code === undefined || e.code === code);
}

/**
 * The sentence a failure is shown with, in a toast or a page's load banner,
 * so one failure reads the same in both. The api's own message is a whole
 * sentence for the codes a person can act on; `internal` carries only
 * "internal error", which says nothing to the reader, so it gets the
 * caller's sentence and what happened instead.
 */
export function errorText(err: unknown, fallback: string): string {
	if (err instanceof ApiError) {
		if (err.code === 'internal')
			return `${fallback} The API failed on its side; try again shortly.`;
		return projectLimitText(err) ?? err.message;
	}
	if (err instanceof NetworkError) return 'Cannot reach the API.';
	return fallback;
}

/**
 * The account's project cap refusal (400 invalid, detail.reason
 * project_limit, DECISIONS I-569) in the CLI's words, so a restore here
 * and `repose run` there say the same; undefined for any other error.
 */
export function projectLimitText(err: unknown): string | undefined {
	if (!(err instanceof ApiError) || err.code !== 'invalid') return undefined;
	const d = err.detail;
	if (d?.reason !== 'project_limit') return undefined;
	const have = Number(d.projects);
	const limit = Number(d.limit);
	const requested = d.requested === undefined ? 1 : Number(d.requested);
	if (requested <= 1)
		return `You have ${have} of the ${limit} projects an account can have, running or stopped. Destroy one first.`;
	return `You have ${have} of the ${limit} projects an account can have, running or stopped, and ${requested} more would make ${have + requested}. Destroy some first.`;
}

/**
 * The codes the api sends with a 503 on purpose (api.md, "Errors";
 * statusOf in internal/api/http/server.go). Each is an answer the page
 * shows in its own words, not an outage: `capacity` (no host can take the
 * machine; Start shows its capacity banner), `waitlisted` (no free seat)
 * and `billing_disabled` (billing is not switched on). Counting them put
 * the outage bar over /billing whenever billing was off (I-390) and over a
 * refused Start next to its capacity banner (I-393).
 */
export const ANSWER_503: readonly ErrorCode[] = ['capacity', 'waitlisted', 'billing_disabled'];

/**
 * Whether a response means the api is in trouble, for the persistent bar
 * (08-dashboard.md 6, "API 5xx or unreachable"): any 5xx except a 503 that
 * carries one of the ANSWER_503 codes.
 */
export function isOutage(status: number, code: ErrorCode | undefined): boolean {
	if (status < 500) return false;
	return !(status === 503 && code !== undefined && ANSWER_503.includes(code));
}
