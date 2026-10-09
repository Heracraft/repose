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
