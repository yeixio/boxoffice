import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { formatLocale } from "../i18n";
import type { Download } from "../api";

// The types a download can be for, in the order the filter shows them:
// what it was grabbed for, or what it was added by hand to be filed as.
// "other" is added by hand with nothing to file it as.
export const downloadTypes: [string, string][] = [
  ["series", "nav.shows"],
  ["movie", "nav.movies"],
  ["album", "nav.music"],
  ["ebook", "nav.books"],
  ["audiobook", "nav.audiobooks"],
  ["game", "nav.games"],
  ["other", "dl.filter.other"],
];

export function typeOf(d: Download): string {
  if (d.kind) return d.kind;
  if (d.fileAs && d.fileAs !== "auto") return d.fileAs;
  return "other";
}

// Where a download in the queue is: checking, repairing, unpacking and
// moving into the library are one, as they're all after downloading and
// one runs at a time.
export const queueStates: [string, string][] = [
  ["downloading", "dl.status.downloading"],
  ["queued", "dl.status.queued"],
  ["paused", "dl.status.paused"],
  ["downloaded", "dl.status.downloaded"],
  ["working", "dl.filter.working"],
];

export function queueState(d: Download): string {
  switch (d.status) {
    case "verifying":
    case "repairing":
    case "unpacking":
    case "importing":
      return "working";
  }
  return d.status;
}

// How a finished download ended; the server works these out, as the
// history comes a page at a time.
export const historyStates: [string, string][] = [
  ["failed", "dl.status.failed"],
  ["quarantined", "dl.quarantined"],
  ["waiting", "dl.filter.waitingLibrary"],
  ["done", "dl.status.completed"],
];

export type Counts = Record<string, number>;

// filterQueue keeps the downloads of a type and in a state ("" for any),
// and counts each type and state for the filters: each with the other
// filter applied, so a count says what picking it would show.
export function filterQueue(list: Download[], type: string, state: string) {
  const types: Counts = {};
  const states: Counts = {};
  const shown: Download[] = [];
  for (const d of list) {
    const ty = typeOf(d);
    const st = queueState(d);
    if (!state || st === state) types[ty] = (types[ty] ?? 0) + 1;
    if (!type || ty === type) states[st] = (states[st] ?? 0) + 1;
    if ((!type || ty === type) && (!state || st === state)) shown.push(d);
  }
  return { shown, types, states };
}

// useFilter is a filter kept in the page's address, so it survives a
// reload and the back button, and can be linked to.
export function useFilter(name: string): [string, (v: string) => void] {
  const [params, setParams] = useSearchParams();
  const value = params.get(name) ?? "";
  const set = (v: string) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p);
        if (v) n.set(name, v);
        else n.delete(name);
        return n;
      },
      { replace: true, preventScrollReset: true },
    );
  return [value, set];
}

// Chips is one row of filter buttons with how many each would show. A
// choice with none is left out, unless it's the one picked. On a phone
// the row scrolls sideways rather than taking up the screen.
export function Chips({
  label,
  options,
  counts,
  value,
  onChange,
}: {
  label: string;
  options: [string, string][];
  counts: Counts;
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const num = new Intl.NumberFormat(formatLocale());
  const all = Object.values(counts).reduce((a, b) => a + b, 0);
  // On a phone the row scrolls, so the picked chip is brought into view
  // (only sideways: the page itself doesn't move).
  const row = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const show = () => {
      const el = row.current;
      const on = el?.querySelector<HTMLElement>('[aria-pressed="true"]');
      if (!el || !on || el.scrollWidth <= el.clientWidth) return;
      const a = on.getBoundingClientRect();
      const b = el.getBoundingClientRect();
      if (a.left < b.left || a.right > b.right)
        el.scrollLeft += a.left - b.left - (b.width - a.width) / 2;
    };
    show();
    // Again once the fonts are in, as they change the chips' widths.
    document.fonts?.ready.then(show);
  }, [value]);
  const chips: [string, string, number][] = [
    ["", t("dl.filter.all"), all],
    ...options
      .filter(([k]) => (counts[k] ?? 0) > 0 || k === value)
      .map(([k, l]): [string, string, number] => [k, t(l), counts[k] ?? 0]),
  ];
  return (
    <div
      ref={row}
      role="group"
      aria-label={label}
      className="-mx-4 flex items-center gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none] sm:mx-0 sm:flex-wrap sm:overflow-visible sm:px-0 sm:pb-0"
    >
      <span className="shrink-0 text-xs font-medium text-muted">{label}</span>
      {chips.map(([k, l, n]) => {
        const on = k === value;
        return (
          <button
            key={k || "all"}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(on ? "" : k)}
            className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 py-1.5 text-sm sm:py-1 ${on ? "border-ink bg-ink text-paper" : "border-line text-muted hover:text-ink"}`}
          >
            {l}
            <span
              className={`tabular-nums text-xs ${on ? "opacity-80" : "opacity-70"}`}
            >
              {num.format(n)}
            </span>
          </button>
        );
      })}
    </div>
  );
}
