import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
} from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";

export interface TerminalHandle {
  write(data: string | Uint8Array): void;
  clear(): void;
  cols: number;
  rows: number;
}

interface Props {
  /** Called with raw keystrokes / paste data from the terminal */
  onData?: (data: string) => void;
  /** Called when the terminal is resized (fit to container) */
  onResize?: (cols: number, rows: number) => void;
}

export const Terminal = forwardRef<TerminalHandle, Props>(
  ({ onData, onResize }, ref) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const xtermRef = useRef<XTerm | null>(null);
    const fitAddonRef = useRef<FitAddon | null>(null);

    useEffect(() => {
      // Defer initialization to a RAF so React StrictMode's synchronous
      // cleanup cycle completes before xterm schedules its own internal RAFs.
      // Without this, xterm's viewport RAF fires on a disposed terminal.
      let rafId: number;
      let term: XTerm | null = null;
      let observer: ResizeObserver | null = null;
      let disposed = false;

      rafId = requestAnimationFrame(() => {
        if (disposed || !containerRef.current) return;

        term = new XTerm({
          theme: {
            background: "#0d1117",
            foreground: "#c9d1d9",
            cursor: "#58a6ff",
            black: "#484f58",
            brightBlack: "#6e7681",
            red: "#ff7b72",
            brightRed: "#ffa198",
            green: "#3fb950",
            brightGreen: "#56d364",
            yellow: "#d29922",
            brightYellow: "#e3b341",
            blue: "#58a6ff",
            brightBlue: "#79c0ff",
            magenta: "#bc8cff",
            brightMagenta: "#d2a8ff",
            cyan: "#39c5cf",
            brightCyan: "#56d4dd",
            white: "#b1bac4",
            brightWhite: "#f0f6fc",
          },
          fontFamily: '"Cascadia Code", "Fira Code", Menlo, monospace',
          fontSize: 14,
          lineHeight: 1.2,
          cursorBlink: true,
          scrollback: 5000,
          allowProposedApi: true,
        });

        const fitAddon = new FitAddon();
        term.loadAddon(fitAddon);
        term.open(containerRef.current!);
        fitAddon.fit();

        xtermRef.current = term;
        fitAddonRef.current = fitAddon;

        if (onData) term.onData(onData);
        term.onResize(({ cols, rows }) => { onResize?.(cols, rows); });

        observer = new ResizeObserver(() => {
          if (!disposed) fitAddon.fit();
        });
        observer.observe(containerRef.current!);
      });

      return () => {
        disposed = true;
        cancelAnimationFrame(rafId);
        observer?.disconnect();
        term?.dispose();
        xtermRef.current = null;
        fitAddonRef.current = null;
      };
    // onData / onResize intentionally omitted — captured via closure on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useImperativeHandle(ref, () => ({
      write(data: string | Uint8Array) {
        xtermRef.current?.write(data);
      },
      clear() {
        xtermRef.current?.clear();
      },
      get cols() {
        return xtermRef.current?.cols ?? 120;
      },
      get rows() {
        return xtermRef.current?.rows ?? 40;
      },
    }));

    return (
      <div
        ref={containerRef}
        className="w-full h-full rounded overflow-hidden"
        style={{ backgroundColor: "#0d1117" }}
      />
    );
  }
);

Terminal.displayName = "Terminal";
