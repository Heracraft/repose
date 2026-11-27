// noVNC ships no types. The dashboard uses the RFB object and nothing
// else of it (DECISIONS I-1216); this is the part of docs/API.md the
// Browser tab calls, against the pinned 1.7.0 in package.json.
declare module '@novnc/novnc' {
	export interface RFBOptions {
		shared?: boolean;
		/** Empty on this path: the stream arrives past SecurityResult. */
		credentials?: Record<string, string>;
		wsProtocols?: string[];
	}

	export default class RFB extends EventTarget {
		constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions);
		background: string;
		clipViewport: boolean;
		compressionLevel: number;
		focusOnClick: boolean;
		qualityLevel: number;
		resizeSession: boolean;
		scaleViewport: boolean;
		showDotCursor: boolean;
		viewOnly: boolean;
		disconnect(): void;
		focus(): void;
		/** Hands the server's clipboard this text (docs/API.md). */
		clipboardPasteFrom(text: string): void;
		/** Sends one key event: an X keysym, its DOM code, down or up. */
		sendKey(keysym: number, code: string | null, down?: boolean): void;
		/**
		 * The server sent clipboard data (docs/API.md "clipboard"). Named
		 * here so the Browser tab's handler is typed; the second signature
		 * keeps the one EventTarget declares.
		 */
		addEventListener(
			type: 'clipboard',
			listener: (e: CustomEvent<{ text: string }>) => void
		): void;
		addEventListener(
			type: string,
			listener: EventListenerOrEventListenerObject | null,
			options?: boolean | AddEventListenerOptions
		): void;
	}
}
