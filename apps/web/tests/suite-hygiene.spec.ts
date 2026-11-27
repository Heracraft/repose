// ENG-106: the suite shares one fake api, so every spec file starts from
// the default hosts by calling cleanSlate(), and this fails a file that
// forgot to.
import { test, expect } from '@playwright/test';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { cleanSlate } from './helpers';

cleanSlate();

test('every spec file starts from the default hosts', () => {
	const dir = dirname(fileURLToPath(import.meta.url));
	const missing = readdirSync(dir)
		.filter((f) => f.endsWith('.spec.ts'))
		.filter((f) => !/^cleanSlate\(\);$/m.test(readFileSync(join(dir, f), 'utf8')));
	expect(missing).toEqual([]);
});
