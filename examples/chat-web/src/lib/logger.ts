type LogLevel = "debug" | "info" | "warn" | "error";
type Meta = Record<string, unknown>;

function send(level: LogLevel, message: string, meta?: Meta): void {
  fetch("/logs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ level, message, ...meta }),
  }).catch(() => {
    // intentionally silent — avoid infinite loops if /logs itself fails
  });
}

export const logger = {
  debug: (message: string, meta?: Meta) => send("debug", message, meta),
  info:  (message: string, meta?: Meta) => send("info",  message, meta),
  warn:  (message: string, meta?: Meta) => send("warn",  message, meta),
  error: (message: string, meta?: Meta) => send("error", message, meta),
};

// Capture unhandled JS errors
window.addEventListener("error", (event) => {
  send("error", event.message, {
    filename: event.filename,
    lineno: event.lineno,
    colno: event.colno,
    stack: event.error?.stack,
  });
});

// Capture unhandled promise rejections
window.addEventListener("unhandledrejection", (event) => {
  const reason = event.reason as { message?: string; stack?: string } | undefined;
  send("error", reason?.message ?? String(event.reason), {
    stack: reason?.stack,
    type: "unhandledrejection",
  });
});
