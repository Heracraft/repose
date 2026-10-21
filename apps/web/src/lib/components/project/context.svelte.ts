// One project's state, shared by its layout and its five tabs (DECISIONS
// I-841). projects/[id]/+layout.svelte creates it, reads the project and
// polls it (10s in view, 60s hidden), and draws the header, the tabs,
// Start and Stop. Each tab reads the project here and fetches only its
// own data, so switching tabs keeps the header, the poll and an op under
// way mounted.
import { getContext, setContext } from 'svelte';
import { toast } from 'svelte-sonner';
import { getOp, getProject, listDestroyed, listProjects, stopProject } from '$lib/api/client';
import { ApiError } from '$lib/api/errors';
import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
import { loadErrorText } from '$lib/components/LoadState.svelte';
import { defaultRestoreName } from '$lib/destroyed';
import { pollUntilDone } from '$lib/poll';
import { projectsStore } from '$lib/projects.svelte';
import type { DestroyedProject, PaymentRequiredReason, Project } from '$lib/api/types';

export type OpKind = 'start' | 'stop' | 'destroy' | 'resize' | 'snapshot' | 'restore';

/** A payment_required refusal, kept whole: api.md's sentence, its reason, and the machines it names. */
export interface Refusal {
	message: string;
	reason?: PaymentRequiredReason;
	projects: { slug: string; id: string }[];
}

const KEY = Symbol('project');

export class ProjectState {
	readonly id: string;
	/** Undefined until the first answer. */
	project = $state<Project | undefined>(undefined);
	/** The api said 404: the project is gone or never was. */
	notFound = $state(false);
	/** The first read failed; the poll keeps trying, and Retry asks now. */
	loadFailed = $state(false);
	loadError = $state<string | undefined>(undefined);
	lastUpdated = $state<Date | undefined>(undefined);
	/** The one operation under way; every action waits on it. */
	opBusy = $state<OpKind | undefined>(undefined);
	/** Start's refusal, shown under the tabs on every tab. */
	startBanner = $state<'payment_required' | 'capacity' | undefined>(undefined);
	refusal = $state<Refusal | undefined>(undefined);
	/**
	 * Config's Apply bar is on screen: the header's Start or Restart draws
	 * secondary there, so the page has one primary.
	 */
	holdPrimary = $state(false);
	/** A destroyed project's row from "Recently destroyed", with a name to restore it under. */
	gone = $state<DestroyedProject | undefined>(undefined);
	goneName = $state('');

	/**
	 * One failure, one report (I-393): the layout's poll and every tab's
	 * share one toast, raised by the first to fail and gone when all of
	 * them get through (I-395).
	 */
	readonly pollFailures = new PollGroup();
	readonly #failure: PollFailure;
	/** Ticks of the poll since the api first said the project is gone. */
	#goneTicks = 0;

	constructor(id: string) {
		this.id = id;
		this.#failure = new PollFailure('Could not load the project.', this.pollFailures);
	}

	/** Takes a fresher copy of the project (a PATCH's answer, a poll). */
	set(p: Project): void {
		this.project = p;
		// Replaced whole on each read, never mutated.
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		this.lastUpdated = new Date();
		// The rail's row follows: its dot is this project's state.
		projectsStore.patch(p);
	}

	/** Reads the project; the layout's poll calls it every tick. */
	refresh = async (): Promise<void> => {
		// Gone: one cheap ask a minute (every sixth 10 s tick), in case it
		// comes back.
		if (this.notFound && ++this.#goneTicks % 6 !== 0) return;
		try {
			this.set(await getProject(this.id));
			this.notFound = false;
			this.loadFailed = false;
			this.#failure.ok();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'not_found') {
				if (!this.notFound) void this.#findDestroyed();
				this.notFound = true;
				return;
			}
			// Before the first answer the banner says why, in the toast's
			// words; after it, what is on screen stays and one toast reports.
			if (!this.project) {
				this.loadFailed = true;
				this.loadError = loadErrorText(err, 'Could not load the project.');
			}
			this.#failure.fail(err, !this.project);
		}
	};

	/** A project that is gone may be one the user destroyed: "Recently destroyed" says when. */
	async #findDestroyed() {
		try {
			const gone = (await listDestroyed()).find((d) => d.id === this.id);
			if (!gone) return;
			const live = await listProjects().catch(() => []);
			this.goneName = defaultRestoreName(
				gone,
				live.map((p) => p.slug)
			);
			this.gone = gone;
		} catch {
			// The 404 stands on its own.
		}
	}

	/** Polls an op until it ends; onDone on success. Resolves true when it ended done. */
	waitForOp = (opId: string, onDone: () => void, projectId = this.id): Promise<boolean> =>
		new Promise((settle) => {
			pollUntilDone(async () => {
				try {
					const op = await getOp(projectId, opId);
					if (op.state === 'done') {
						onDone();
						settle(true);
						return true;
					}
					if (op.state === 'error') {
						toast.error(op.error ?? 'The operation failed.');
						this.opBusy = undefined;
						settle(false);
						return true;
					}
					return false;
				} catch (err) {
					toastApiError(err);
					this.opBusy = undefined;
					settle(false);
					return true;
				}
			});
		});

	/**
	 * Shows a payment_required refusal (Start, or a resize past the plan's
	 * disk) under the tabs: its sentence, its reason, and for plan_limit
	 * the machines using the memory, each with a Stop.
	 */
	refuse = async (err: ApiError): Promise<void> => {
		const detail = err.detail ?? {};
		const reason =
			typeof detail.reason === 'string' ? (detail.reason as PaymentRequiredReason) : undefined;
		const slugs = Array.isArray(detail.projects)
			? detail.projects.filter((s): s is string => typeof s === 'string')
			: [];
		let named: { slug: string; id: string }[] = [];
		if (slugs.length > 0) {
			try {
				const mine = await listProjects();
				named = slugs.flatMap((slug) => {
					const p = mine.find((q) => q.slug === slug);
					return p ? [{ slug, id: p.id }] : [];
				});
			} catch {
				// The sentence still names them.
			}
		}
		this.startBanner = 'payment_required';
		this.refusal = { message: err.message, reason, projects: named };
	};

	/** Stops a machine the refusal named, so this one can start. */
	stopNamed = async (target: { slug: string; id: string }): Promise<void> => {
		this.opBusy = 'stop';
		try {
			const { op_id } = await stopProject(target.id, true);
			void this.waitForOp(
				op_id,
				() => {
					this.opBusy = undefined;
					if (this.refusal)
						this.refusal = {
							...this.refusal,
							projects: this.refusal.projects.filter((p) => p.id !== target.id)
						};
					toast.success(`Stopped ${target.slug}.`);
				},
				target.id
			);
		} catch (err) {
			this.opBusy = undefined;
			toastApiError(err, `Could not stop ${target.slug}.`);
		}
	};
}

/** Called once, by the project layout. */
export function provideProject(state: ProjectState): ProjectState {
	return setContext(KEY, state);
}

/** The project the layout above this tab holds. */
export function useProject(): ProjectState {
	const s = getContext<ProjectState | undefined>(KEY);
	if (!s) throw new Error('useProject() outside projects/[id]/+layout.svelte');
	return s;
}
