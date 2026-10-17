// Start and Stop from a row of /projects. The same calls and the same op
// wait as the project page's onStart and stopNamed (copied, not imported:
// a route's script is not a module other pages can take from). Stop keeps
// its snapshot, as the CLI's `repose stop` does. A plan or capacity
// refusal is rethrown for the caller to draw as a banner: it names the
// machines holding the memory and offers to stop one, and a toast that
// vanishes with a raw URL in it gave the user nothing to act on.
import { toast } from 'svelte-sonner';
import { getOp, startProject, stopProject } from '$lib/api/client';
import { ApiError } from '$lib/api/errors';
import { toastApiError } from '$lib/api/toast';

/** The refusals a caller explains itself, rather than toasting. */
export const EXPLAINED: readonly string[] = ['payment_required', 'capacity'];
import { pollUntilDone } from '$lib/poll';

export type RowAction = 'start' | 'stop';

/** Polls the op every 2s until it ends; true when it ended done. */
function waitForOp(projectId: string, opId: string): Promise<boolean> {
	return new Promise((settle) => {
		pollUntilDone(async () => {
			try {
				const op = await getOp(projectId, opId);
				if (op.state === 'done') {
					settle(true);
					return true;
				}
				if (op.state === 'error') {
					toast.error(op.error ?? 'The operation failed.');
					settle(false);
					return true;
				}
				return false;
			} catch (err) {
				toastApiError(err);
				settle(false);
				return true;
			}
		});
	});
}

/**
 * Runs Start or Stop on one project and waits for its op. Resolves when
 * it has ended either way; errors are toasted here, so the caller only
 * clears its pending label and refreshes.
 */
export async function runRowAction(
	action: RowAction,
	project: { id: string; name: string }
): Promise<boolean> {
	try {
		const { op_id } =
			action === 'start' ? await startProject(project.id) : await stopProject(project.id, true);
		return await waitForOp(project.id, op_id);
	} catch (err) {
		if (err instanceof ApiError && EXPLAINED.includes(err.code)) throw err;
		toastApiError(
			err,
			action === 'start' ? `Could not start ${project.name}.` : `Could not stop ${project.name}.`
		);
		return false;
	}
}
