// The five agents every machine ships (docs/features/agents.md), as the
// dashboard names them: the id is the binary, the CLI's `--agent` and the
// api's `agent_default`; the name is the product's own (I-1702).
export const AGENTS: { id: string; name: string }[] = [
	{ id: 'claude', name: 'Claude Code' },
	{ id: 'codex', name: 'Codex' },
	{ id: 'opencode', name: 'opencode' },
	{ id: 'gemini', name: 'Gemini CLI' },
	{ id: 'pi', name: 'pi' }
];

/** "Claude Code" for claude; an id that is not one of the five is itself. */
export const agentName = (id: string) => AGENTS.find((a) => a.id === id)?.name ?? id;
