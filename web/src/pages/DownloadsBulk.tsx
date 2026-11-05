import i18n from "../i18n";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownToLine,
  ArrowUpToLine,
  FolderInput,
  PackageOpen,
  Pause,
  Play,
  RotateCcw,
  Trash2,
  X,
} from "lucide-react";
import { post, type Download } from "../api";
import { Button, ErrorNote, Modal, Toggle } from "../ui";

export type BulkList = "queue" | "history";
export type BulkAction =
  | "pause"
  | "resume"
  | "retry"
  | "import"
  | "reprocess"
  | "top"
  | "bottom"
  | "remove";

type BulkResult = { done: number; failed: { id: number; error: string }[] };

// Being checked, unpacked or moved: its files are in use, so it's locked.
const working = (d: Download) =>
  ["verifying", "repairing", "unpacking", "importing"].includes(d.status);

// Which of the picked downloads each action applies to. A button only
// shows when it applies to at least one, and only acts on those.
const applies: Record<
  BulkList,
  [BulkAction, string, (d: Download) => boolean][]
> = {
  queue: [
    [
      "pause",
      "dlBulk.pause",
      (d) => d.status === "queued" || d.status === "downloading",
    ],
    ["resume", "dlBulk.resume", (d) => d.status === "paused"],
    [
      "top",
      "dlBulk.top",
      (d) => d.status === "queued" || d.status === "paused",
    ],
    [
      "bottom",
      "dlBulk.bottom",
      (d) => d.status === "queued" || d.status === "paused",
    ],
    ["remove", "dlBulk.remove", (d) => !working(d)],
  ],
  history: [
    [
      "import",
      "dlBulk.import",
      (d) => d.status === "completed" && !!d.canImport,
    ],
    ["reprocess", "dlBulk.reprocess", (d) => !!d.canReprocess],
    [
      "retry",
      "dlBulk.retry",
      (d) => d.status !== "completed" && !d.quarantined,
    ],
    ["remove", "dlBulk.remove", () => true],
  ],
};

const icons: Record<BulkAction, React.ReactNode> = {
  pause: <Pause className="size-4" aria-hidden />,
  resume: <Play className="size-4" aria-hidden />,
  top: <ArrowUpToLine className="size-4" aria-hidden />,
  bottom: <ArrowDownToLine className="size-4" aria-hidden />,
  remove: <Trash2 className="size-4" aria-hidden />,
  import: <FolderInput className="size-4" aria-hidden />,
  reprocess: <PackageOpen className="size-4" aria-hidden />,
  retry: <RotateCcw className="size-4" aria-hidden />,
};

// SelectAll is the checkbox in a section's header while picking.
export function SelectAll({
  items,
  picked,
  onChange,
  paged,
}: {
  items: Download[];
  picked: Set<number>;
  onChange: (ids: Set<number>) => void;
  paged?: boolean;
}) {
  const { t } = useTranslation();
  const n = items.filter((d) => picked.has(d.id)).length;
  return (
    <label className="flex min-h-9 cursor-pointer items-center gap-2 text-sm">
      <input
        type="checkbox"
        className="size-5 accent-[var(--marquee)]"
        checked={n > 0 && n === items.length}
        ref={(el) => {
          if (el) el.indeterminate = n > 0 && n < items.length;
        }}
        onChange={(e) =>
          onChange(new Set(e.target.checked ? items.map((d) => d.id) : []))
        }
      />
      {n > 0 ? t("quarantine.selected", { count: n }) : paged ? t("dlBulk.selectPage") : t("quarantine.selectAll")}
    </label>
  );
}

// PickBox is a row's checkbox while picking.
export function PickBox({
  d,
  picked,
  onToggle,
}: {
  d: Download;
  picked: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  return (
    <label className="-m-2 flex shrink-0 cursor-pointer items-center p-2">
      <input
        type="checkbox"
        className="size-5 accent-[var(--marquee)]"
        checked={picked}
        onChange={onToggle}
        aria-label={t("quarantine.selectName", { name: d.name })}
      />
    </label>
  );
}

// BulkBar sits at the bottom of the screen while downloads are picked,
// with what can be done to them.
export function BulkBar({
  list,
  items,
  picked,
  onDone,
}: {
  list: BulkList;
  items: Download[];
  picked: Set<number>;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [removing, setRemoving] = useState(false);
  const [note, setNote] = useState("");
  const chosen = items.filter((d) => picked.has(d.id));
  const run = useMutation({
    mutationFn: ({
      action,
      ids,
      deleteFiles,
      block,
    }: {
      action: BulkAction;
      ids: number[];
      deleteFiles?: boolean;
      block?: boolean;
    }) =>
      post<BulkResult>("/downloads/bulk", {
        action,
        ids,
        deleteFiles: !!deleteFiles,
        block: !!block,
      }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["downloads"] });
      setRemoving(false);
      if (r.failed.length === 0) {
        setNote("");
        onDone();
        return;
      }
      const name = (id: number) =>
        items.find((d) => d.id === id)?.name ?? `#${id}`;
      setNote(
        `${r.done > 0 ? `${i18n.t("dlBulk.done", { count: r.done })} ` : ""}` +
          i18n.t("quarantine.couldnt", {
            count: r.failed.length,
            which:
              r.failed
                .slice(0, 3)
                .map((f) => `${name(f.id)} (${f.error})`)
                .join("; ") +
              (r.failed.length > 3 ? i18n.t("dlBulk.andMore", { count: r.failed.length - 3 }) : ""),
          }),
      );
    },
  });
  if (chosen.length === 0) return null;
  // Downloads are listed in the order they're shown, so moving several to
  // the top or bottom keeps them in that order.
  const ids = (ok: (d: Download) => boolean) =>
    chosen.filter(ok).map((d) => d.id);
  return (
    <div
      className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-card/95 shadow-[0_-4px_16px_rgba(0,0,0,0.25)] backdrop-blur"
      style={{ paddingBottom: "env(safe-area-inset-bottom)" }}
    >
      <div className="mx-auto max-w-7xl px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="me-auto text-sm font-medium sm:me-2">
            {t("quarantine.selected", { count: chosen.length })}
          </span>
          <button
            onClick={onDone}
            title={t("quarantine.stop")}
            aria-label={t("quarantine.stop")}
            className="rounded-md p-2 text-muted hover:bg-line hover:text-ink sm:order-last"
          >
            <X className="size-5" />
          </button>
          <div className="flex basis-full flex-wrap gap-2 sm:flex-1 sm:basis-auto">
            {applies[list].map(([action, label, ok]) => {
              const n = ids(ok).length;
              if (n === 0) return null;
              const busy = run.isPending && run.variables?.action === action;
              return (
                <Button
                  key={action}
                  variant={action === "remove" ? "danger" : "plain"}
                  className="whitespace-nowrap px-3 py-1.5"
                  busy={busy}
                  disabled={run.isPending}
                  onClick={() =>
                    action === "remove"
                      ? setRemoving(true)
                      : run.mutate({ action, ids: ids(ok) })
                  }
                >
                  {!busy && icons[action]}
                  {t(label)}
                  {n < chosen.length && (
                    <span className="text-muted">({n})</span>
                  )}
                </Button>
              );
            })}
          </div>
        </div>
        {note && <p className="mt-2 break-words text-xs text-bad">{note}</p>}
        <ErrorNote error={run.error} />
      </div>
      {removing && (
        <BulkRemove
          items={chosen.filter(applies[list].find(([a]) => a === "remove")![2])}
          queue={list === "queue"}
          busy={run.isPending}
          error={run.error}
          onRemove={(ids, deleteFiles, block) =>
            run.mutate({ action: "remove", ids, deleteFiles, block })
          }
          onClose={() => setRemoving(false)}
        />
      )}
    </div>
  );
}

function BulkRemove({
  items,
  queue,
  busy,
  error,
  onRemove,
  onClose,
}: {
  items: Download[];
  queue: boolean;
  busy: boolean;
  error: Error | null;
  onRemove: (ids: number[], deleteFiles: boolean, block: boolean) => void;
  onClose: () => void;
}) {
  const withFiles = items.filter((d) => d.folder && !d.quarantined);
  const held = items.filter((d) => d.quarantined).length;
  // As for one: files go by default when there's nothing in them to keep,
  // that is when it failed or is already in the library.
  const [files, setFiles] = useState(
    withFiles.length > 0 &&
      withFiles.every((d) => d.status !== "completed" || !!d.importedTo),
  );
  // Off the queue they're blocked unless you say otherwise, as for one.
  const [block, setBlock] = useState(queue);
  const { t } = useTranslation();
  const forTitles = items.some((d) => !!d.kind);
  return (
    <Modal
      title={t("dlBulk.removeTitle", { count: items.length })}
      onClose={onClose}
    >
      <ul className="mb-4 max-h-48 overflow-y-auto text-sm">
        {items.map((d) => (
          <li key={d.id} className="truncate" title={d.name}>
            {d.name}
          </li>
        ))}
      </ul>
      <div className="space-y-3">
        {withFiles.length > 0 && (
          <Toggle
            checked={files}
            onChange={setFiles}
            label={
              <>
                {t("dlBulk.alsoDelete")}
                <span className="block text-xs text-muted">
                  {t("dlBulk.neverTouched")}
                </span>
              </>
            }
          />
        )}
        {forTitles && (
          <BlockToggle
            checked={block}
            onChange={setBlock}
            many={items.length > 1}
          />
        )}
      </div>
      {held > 0 && (
        <p className="mt-3 text-sm text-muted">
          {t("dlBulk.heldStay")}
        </p>
      )}
      <ErrorNote error={error} />
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>{t("dlBulk.keep")}</Button>
        <Button
          variant="danger"
          busy={busy}
          onClick={() =>
            onRemove(
              items.map((d) => d.id),
              files,
              block && forTitles,
            )
          }
        >
          {t("dlBulk.remove")}
        </Button>
      </div>
    </Modal>
  );
}

// BlockToggle is the choice, when removing, to put the release on the
// blocklist so searches skip it.
export function BlockToggle({
  checked,
  onChange,
  many,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  many?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Toggle
      checked={checked}
      onChange={onChange}
      label={
        <>
          {many ? t("dlBulk.blockMany") : t("dlBulk.blockOne")}
          <span className="block text-xs text-muted">
            {t("dlBulk.blockHint")}
          </span>
        </>
      }
    />
  );
}
