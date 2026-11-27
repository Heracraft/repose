// What an earlier menu holds, for the Config tab's read-only line
// (DECISIONS I-1698): the menu that `repose config add` and the tab used
// to fill is gone, and a project that has entries keeps them, installed
// again at every base update.
import type { MenuItem } from './api/types';

/**
 * The entries of a stored selection as the tab lists them: a menu entry
 * by its id with its option's value after it (`postgresql 16`), a package
 * by its name. Anything that is not the documented array reads as empty.
 */
export function menuEntries(sel: unknown): string[] {
	if (!Array.isArray(sel)) return [];
	const out: string[] = [];
	for (const item of sel as MenuItem[]) {
		if (item && 'package' in item && typeof item.package === 'string') {
			out.push(item.package);
		} else if (item && 'id' in item && typeof item.id === 'string') {
			const value = Object.values(item.options ?? {})[0];
			out.push(value === undefined ? item.id : `${item.id} ${value}`);
		}
	}
	return out;
}
