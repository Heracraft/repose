// Start and Stop from a row of /projects. The same calls and the same op
// wait as the project page's onStart and stopNamed (copied, not imported:
// a route's script is not a module other pages can take from). Stop keeps
// its snapshot, as the CLI's `repose stop` does. A refusal is a toast: the
// project page is where a payment_required or capacity refusal is
// explained in full.
import { toast } from 'svelte-sonner';
import { getOp, startProject, stopProject } from '$lib/api/client';
import { toastApiError } from '$lib/api/toast';
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
		toastApiError(
			err,
			action === 'start' ? `Could not start ${project.name}.` : `Could not stop ${project.name}.`
		);
		return false;
	}
}
