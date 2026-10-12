// Looks the dashboard draws only behind a flag, for things the api does not
// send yet. Each one is on with the dev server or its PUBLIC_FLAG_* set to
// 1 where the dashboard runs; production sets none.
import { dev } from '$app/environment';
import { env } from '$env/dynamic/public';

/** A frozen machine (DECISIONS I-683): `project.resting`, a field the
 * "Resting idle machines" proposal suggests and only the fake api sends. */
export const FROZEN_LOOK = dev || env.PUBLIC_FLAG_FROZEN === '1';
