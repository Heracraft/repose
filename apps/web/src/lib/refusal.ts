/**
 * The api's payment_required sentence ends by naming the billing page's
 * URL (the CLI prints it as is). The dashboard's banner has its own link
 * to that page, so the URL, and an "upgrade" clause that only points at
 * it, come out here and the sentence keeps its facts.
 */
export function withoutBillingURL(message: string): string {
	return message
		.replace(/,? or upgrade at https?:\/\/\S+\.$/, '.')
		.replace(/ ?Upgrade(?: to \w+)? at https?:\/\/\S+\.$/, '')
		.replace(/ at https?:\/\/\S*?(?=\.?(?:\s|$))/g, '')
		.trim();
}

/**
 * The api's refusal in the dashboard's words: its second way out names
 * the CLI's flag (I-1654), and here the same choice is New project's Runs
 * on, so the clause says that instead of showing a command in backticks
 * (pass2-dash N2).
 */
export function dashboardWords(message: string): string {
	return message
		.replace(/, or run it on repose's servers: repose run --host repose/, ", or make it on repose's servers with New project")
		.replace(/, or `repose run --host repose` makes this one on repose's servers/, ", or make it on repose's servers with New project");
}
