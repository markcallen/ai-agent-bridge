interface Props {
  children: React.ReactNode;
}

function isMobile(): boolean {
  return /Mobi|Android|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(
    navigator.userAgent
  ) || window.matchMedia("(pointer: coarse)").matches;
}

export function MobileGate({ children }: Props) {
  if (!isMobile()) return <>{children}</>;

  return (
    <div className="flex flex-col items-center justify-center h-screen bg-gray-900 text-gray-100 px-8 text-center">
      <div className="text-5xl mb-6">🖥️</div>
      <h1 className="text-2xl font-semibold mb-3">Desktop only</h1>
      <p className="text-gray-400 max-w-sm">
        This app requires a physical keyboard to interact with the terminal.
        Please open it on a desktop or laptop browser.
      </p>
    </div>
  );
}
