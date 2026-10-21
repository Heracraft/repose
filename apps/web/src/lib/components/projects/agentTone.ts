// An agent's state as the dot beside its name (Guide B 5.3, I-837): the
// list's chips and the project page's Agents rows draw the same dot. A
// turn under way pulses, waiting on you is amber, idle is the hollow
// ring, an agent without turn hooks that is only `running` (I-694) is the
// filled ink dot, and a word the api adds later reads as idle.
export type AgentTone = 'busy' | 'warn' | 'idle' | 'on';

export function agentTone(state: string): AgentTone {
	if (state === 'working') return 'busy';
	if (state === 'needs_input' || state === 'question') return 'warn';
	if (state === 'running') return 'on';
	return 'idle';
}
