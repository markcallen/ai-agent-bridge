import { useRef, useState } from "react";

interface Props {
  disabled?: boolean;
  onSubmit: (text: string) => void;
}

export function InputBar({ disabled, onSubmit }: Props) {
  const [value, setValue] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  function submit() {
    const text = value;
    if (!text) return;
    setValue("");
    onSubmit(text);
    inputRef.current?.focus();
  }

  return (
    <div className="flex items-center gap-2 px-4 py-2 bg-gray-800 border-t border-gray-700">
      <input
        ref={inputRef}
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
        }}
        disabled={disabled}
        placeholder={disabled ? "Start a session to send input…" : "Type a message and press Enter"}
        className="flex-1 bg-gray-700 text-gray-100 text-sm rounded px-3 py-2 border border-gray-600 placeholder-gray-500 focus:outline-none focus:border-blue-500 disabled:opacity-40 disabled:cursor-not-allowed"
      />
      <button
        onClick={submit}
        disabled={disabled || !value}
        className="px-4 py-2 text-sm font-medium rounded bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 disabled:cursor-not-allowed transition-colors"
      >
        Send
      </button>
    </div>
  );
}
