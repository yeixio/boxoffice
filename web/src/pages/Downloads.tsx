import { Trans, useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  ArrowUpToLine,
  CircleCheck,
  ListChecks,
  Clock,
  FolderInput,
  CircleX,
  Download as DownloadIcon,
  EyeOff,
  Pause,
  Play,
  PackageOpen,
  RotateCcw,
  ShieldAlert,
  Trash2,
  Upload,
} from "lucide-react";
import {
  del,
  get,
  post,
  put,
  type Download,
  type Downloads as DownloadsData,
} from "../api";
import { bytes, titleLink, when } from "../format";
import i18n, { formatLocale } from "../i18n";
import { useQualities } from "../hooks";
import { SortControl, useSort, words, type SortOption } from "../sort";
import {
  Badge,
  Button,
  Card,
  Empty,
  ErrorNote,
  Heading,
  Input,
  Modal,
  Spinner,
  Toggle,
} from "../ui";
import {
  BlockToggle,
  BulkBar,
  PickBox,
  SelectAll,
  type BulkList,
} from "./DownloadsBulk";
import { DownloadFiles } from "./DownloadFiles";
import { Quarantine } from "./DownloadsQuarantine";
import { Pager, usePageSize } from "./Pager";
import { AddNzb, isNzb } from "../AddNzb";

// HistoryPage is which page of the history to fetch, sorted how; the
// server pages and sorts it, as it can grow long.
type HistoryPage = {
  offset: number;
  limit: number;
  sort: string;
  dir: "asc" | "desc";
  // Show failures for articles gone from the news servers, hidden as
  // there's nothing to do about them.
  missing?: boolean;
  // Only those whose name has every word of this.
  query?: string;
};

// nameWords splits a search, or a release name, into lowercase words: dots,
// underscores and dashes stand for spaces in release names.
export function nameWords(s: string) {
  return s
    .toLowerCase()
    .split(/[\s._-]+/)
    .filter(Boolean);
}

// matches says whether a release name has every word of a search.
export function matches(name: string, query: string) {
  const n = nameWords(name).join(" ");
  return nameWords(query).every((w) => n.includes(w));
}

// useSettled is value once it has stopped changing for a moment, so a
// search isn't sent for every key pressed.
function useSettled<T>(value: T, ms = 250) {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return settled;
}

export function useDownloads(page?: HistoryPage) {
  const q = page
    ? `?historyOffset=${page.offset}&historyLimit=${page.limit}&historySort=${page.sort}&historyDir=${page.dir}${page.missing ? "&historyMissing=true" : ""}${page.query ? `&historyQuery=${encodeURIComponent(page.query)}` : ""}`
    : "";
  return useQuery({
    queryKey: ["downloads", page ?? null],
    queryFn: () => get<DownloadsData>("/downloads" + q),
    refetchInterval: 2000,
    placeholderData: keepPreviousData,
  });
}

const statusTone: Record<string, "ok" | "bad" | "gold" | "plain" | "marquee"> =
  {
    downloading: "marquee",
    downloaded: "gold",
    verifying: "gold",
    repairing: "gold",
    unpacking: "gold",
    importing: "gold",
    queued: "plain",
    paused: "plain",
    completed: "ok",
    failed: "bad",
  };

const statusName = (status: string) =>
  i18n.t(`web:dl.status.${status}`, { defaultValue: status });

export function speed(n: number) {
  return i18n.t("web:dl.speed", { size: bytes(n) });
}

function eta(left: number, rate: number) {
  const time = duration(left, rate);
  return time ? i18n.t("web:dl.eta.left", { time }) : "";
}

function duration(left: number, rate: number) {
  if (rate <= 0 || left <= 0) return "";
  const s = left / rate;
  if (s < 60) return i18n.t("web:dl.eta.underMinute");
  if (s < 3600)
    return i18n.t("web:dl.eta.minutes", { minutes: Math.round(s / 60) });
  const h = Math.floor(s / 3600);
  if (h >= 48)
    return i18n.t("web:dl.eta.days", {
      days: Math.floor(h / 24),
      hours: h % 24,
    });
  return i18n.t("web:dl.eta.hours", {
    hours: h,
    minutes: Math.round((s % 3600) / 60),
  });
}

// What needs a look comes first: failed, then held in quarantine, then
// waiting to go into the library, then done.
function outcome(d: Download): number {
  if (d.status !== "completed") return 0;
  if (d.quarantined) return 1;
  if (d.canImport) return 2;
  return 3;
}

// History sorts; the queue keeps its own order, which is what downloads next.
const historySorts: SortOption<Download>[] = [
  {
    key: "finished",
    label: "dl.sort.finished",
    value: (d) => d.finishedAt ?? d.addedAt,
    dir: "desc",
    says: words.date,
  },
  {
    key: "added",
    label: "sorts.added",
    value: (d) => d.addedAt,
    dir: "desc",
    says: words.date,
  },
  {
    key: "name",
    label: "sorts.name",
    value: (d) => d.name,
    dir: "asc",
    says: words.text,
  },
  {
    key: "size",
    label: "dl.sort.size",
    value: (d) => d.size || undefined,
    dir: "desc",
    says: words.size,
  },
  {
    key: "status",
    label: "sorts.status",
    value: outcome,
    dir: "asc",
    says: { asc: "dl.sort.problems", desc: "dl.sort.done" },
  },
];

export function Downloads() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const sort = useSort("downloads-history", historySorts);
  // A few rows at a time on a phone, more on a bigger screen.
  const queueSize = usePageSize([4, 6, 8]);
  const historySize = usePageSize([6, 10, 15]);
  const [queuePage, setQueuePage] = useState(0);
  const [historyPage, setHistoryPage] = useState(0);
  const [showMissing, setShowMissing] = useState(false);
  const [queueQuery, setQueueQuery] = useState("");
  const [historyQuery, setHistoryQuery] = useState("");
  const historySearch = useSettled(historyQuery.trim());
  useEffect(
    () => setHistoryPage(0),
    [sort.option.key, sort.dir, historySize, showMissing, historySearch],
  );
  useEffect(() => setQueuePage(0), [queueQuery]);
  const d = useDownloads({
    offset: historyPage * historySize,
    limit: historySize,
    sort: sort.option.key,
    dir: sort.dir,
    missing: showMissing,
    query: historySearch,
  });
  // A page past the end (the history got shorter) goes back to the last.
  const total = d.data?.historyTotal ?? 0;
  useEffect(() => {
    if (historyPage > 0 && historyPage * historySize >= total)
      setHistoryPage(Math.max(0, Math.ceil(total / historySize) - 1));
  }, [total, historyPage, historySize]);
  const refresh = () => qc.invalidateQueries({ queryKey: ["downloads"] });
  const settings = useMutation({
    mutationFn: (s: Partial<DownloadsData["settings"]>) =>
      put("/downloads/settings", { ...d.data!.settings, ...s }),
    onSuccess: refresh,
  });
  // Adding an NZB by hand: true opens the window, a File (dropped on the
  // page) opens it reading that file.
  const [adding, setAdding] = useState<boolean | File>(false);
  useEffect(() => {
    // An NZB dropped anywhere on the page is added.
    const over = (e: DragEvent) => {
      if (e.dataTransfer?.types.includes("Files")) e.preventDefault();
    };
    const drop = (e: DragEvent) => {
      const f = e.dataTransfer?.files[0];
      if (f && isNzb(f)) {
        e.preventDefault();
        setAdding(f);
      }
    };
    window.addEventListener("dragover", over);
    window.addEventListener("drop", drop);
    return () => {
      window.removeEventListener("dragover", over);
      window.removeEventListener("drop", drop);
    };
  }, []);
  const clear = useMutation({
    mutationFn: () => del("/downloads/history"),
    onSuccess: refresh,
  });
  // Picking several downloads at once, in one list at a time.
  const [picking, setPicking] = useState<BulkList | null>(null);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const startPicking = (list: BulkList) => {
    setPicking(list);
    setPicked(new Set());
  };
  const stopPicking = () => {
    setPicking(null);
    setPicked(new Set());
  };
  const toggle = (id: number) =>
    setPicked((p) => {
      const n = new Set(p);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  if (d.isPending) return <Spinner />;
  if (d.isError) return <ErrorNote error={d.error} />;
  const data = d.data;
  const s = data.settings;
  const active = data.queue.find((x) => x.status === "downloading");
  const history = data.history;
  const historyTotal = data.historyTotal ?? history.length;
  const historyHidden = data.historyHidden ?? 0;
  // The queue's own pages; a page past the end (the queue got shorter)
  // shows the last one.
  const shownQueue = queueQuery.trim()
    ? data.queue.filter((x) => matches(x.name, queueQuery))
    : data.queue;
  const queuePages = Math.max(1, Math.ceil(shownQueue.length / queueSize));
  const qp = Math.min(queuePage, queuePages - 1);
  const queue = shownQueue.slice(qp * queueSize, (qp + 1) * queueSize);
  // Turning the page drops what was picked: Select all picks a page.
  const turn = (set: (p: number) => void) => (p: number) => {
    set(p);
    setPicked(new Set());
  };
  const pickedList =
    picking === "queue" ? queue : picking === "history" ? history : [];
  const pickHead = (list: BulkList, items: Download[], paged: boolean) =>
    items.length > 0 &&
    (picking === list ? (
      <>
        <SelectAll
          items={items}
          picked={picked}
          onChange={setPicked}
          paged={paged}
        />
        <Button variant="ghost" onClick={stopPicking}>
          {t("bulk.done")}
        </Button>
      </>
    ) : (
      <Button variant="ghost" onClick={() => startPicking(list)}>
        <ListChecks className="size-4" aria-hidden /> {t("list.select")}
      </Button>
    ));
  return (
    <div className={picking && picked.size > 0 ? "pb-44 sm:pb-28" : ""}>
      <Heading
        title={t("dl.title")}
        sub={
          s.enabled ? (
            s.paused ? (
              t("dl.allPaused")
            ) : (
              <>
                {data.speed > 0
                  ? speed(data.speed)
                  : data.waiting && data.queue.length > 0
                    ? t("dl.waiting", { reason: data.waiting })
                    : t("dl.idle")}
                {data.queue.length > 0 &&
                  ` · ${t("dl.inQueue", { count: data.queue.length })}`}
              </>
            )
          ) : (
            t("dl.off")
          )
        }
        actions={
          <>
            <Button onClick={() => setAdding(true)}>
              <Upload className="size-4" aria-hidden /> {t("dl.addNzb")}
            </Button>
            {s.enabled && (
              <Button
                variant={s.paused ? "primary" : "plain"}
                busy={settings.isPending}
                onClick={() => settings.mutate({ paused: !s.paused })}
              >
                {s.paused ? (
                  <Play className="size-4" aria-hidden />
                ) : (
                  <Pause className="size-4" aria-hidden />
                )}
                {s.paused ? t("dl.resumeAll") : t("dl.pauseAll")}
              </Button>
            )}
          </>
        }
      />
      {s.enabled && <Stats data={data} />}
      <ErrorNote error={settings.error} />
      {adding && (
        <AddNzb
          file={adding instanceof File ? adding : undefined}
          onClose={() => setAdding(false)}
        />
      )}
      {!s.enabled && <TurnOn data={data} />}
      {s.enabled && data.folder.problem && (
        <p
          role="alert"
          className="mb-4 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad"
        >
          {data.folder.problem}
        </p>
      )}
      {!!data.servers?.some((x) => x.error) && (
        <div className="mb-4 rounded-md border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
          {data
            .servers!.filter((x) => x.error)
            .map((x) => (
              <p key={x.id}>
                <Trans
                  i18nKey="dl.serverError"
                  values={{ name: x.name, error: x.error }}
                  components={{
                    link: (
                      <Link to="/settings#news-servers" className="underline" />
                    ),
                  }}
                />
              </p>
            ))}
        </div>
      )}

      <section className="mb-8">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="me-auto font-display text-xl font-semibold">
            {t("dl.queue")}
          </h2>
          {data.queue.length > 1 && (
            <Search value={queueQuery} onChange={setQueueQuery} />
          )}
          {pickHead("queue", queue, queuePages > 1)}
        </div>
        {data.queue.length > 0 && shownQueue.length === 0 ? (
          <p className="text-sm text-muted">
            {t("dl.noMatch", { query: queueQuery.trim() })}
          </p>
        ) : data.queue.length === 0 ? (
          <Empty
            icon={<DownloadIcon className="size-8" />}
            title={t("dl.nothing")}
          >
            {t("dl.nothingHint")}
          </Empty>
        ) : (
          <ul className="flex flex-col gap-3">
            {queue.map((x) => (
              <QueueItem
                key={x.id}
                d={x}
                rate={x.id === active?.id ? data.speed : 0}
                first={x.id === data.queue[0].id}
                pick={
                  picking === "queue"
                    ? { picked: picked.has(x.id), toggle: () => toggle(x.id) }
                    : undefined
                }
              />
            ))}
          </ul>
        )}
        <Pager
          page={qp}
          size={queueSize}
          total={shownQueue.length}
          onPage={turn(setQueuePage)}
          label={t("dl.queue")}
        />
      </section>

      <section>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="me-auto font-display text-xl font-semibold">
            {t("dl.history")}
          </h2>
          {(historyTotal > 1 || historyQuery) && (
            <Search value={historyQuery} onChange={setHistoryQuery} />
          )}
          {historyTotal > 1 && (
            <SortControl
              sort={sort}
              className="order-last w-full sm:order-none sm:w-auto"
            />
          )}
          {(historyHidden > 0 || showMissing) && (
            <Button
              variant="ghost"
              title={t("dl.missingHint")}
              aria-pressed={showMissing}
              onClick={() => setShowMissing((v) => !v)}
            >
              <EyeOff className="size-4" aria-hidden />
              {showMissing
                ? t("dl.hideMissing")
                : t("dl.showMissing", { count: historyHidden })}
            </Button>
          )}
          {pickHead("history", history, historyTotal > historySize)}
          {data.history.length > 0 && picking !== "history" && (
            <Button
              variant="ghost"
              busy={clear.isPending}
              onClick={() => clear.mutate()}
            >
              {t("dl.clear")}
            </Button>
          )}
        </div>
        {data.history.length === 0 ? (
          <p className="text-sm text-muted">
            {historySearch
              ? t("dl.noMatch", { query: historySearch })
              : t("dl.noHistory")}
          </p>
        ) : (
          <ul className="divide-y divide-line rounded-xl border border-line bg-card">
            {history.map((x) => (
              <HistoryItem
                key={x.id}
                d={x}
                pick={
                  picking === "history"
                    ? { picked: picked.has(x.id), toggle: () => toggle(x.id) }
                    : undefined
                }
              />
            ))}
          </ul>
        )}
        <Pager
          page={historyPage}
          size={historySize}
          total={historyTotal}
          onPage={turn(setHistoryPage)}
          label={t("dl.history")}
        />
      </section>
      <Quarantine />
      <DownloadFiles />
      {data.folder.path && (
        <p className="mt-6 text-xs text-muted">
          {t(data.folder.freeSpace > 0 ? "dl.folderFree" : "dl.folder", {
            path: data.folder.path,
            free: bytes(data.folder.freeSpace),
          })}
        </p>
      )}
      {picking && (
        <BulkBar
          list={picking}
          items={pickedList}
          picked={picked}
          onDone={stopPicking}
        />
      )}
    </div>
  );
}

type Pick = { picked: boolean; toggle: () => void };

// Stats sums up the queue: how fast, how much is left and how long that
// takes at this speed, and how much has come this month.
function Stats({ data }: { data: DownloadsData }) {
  const { t } = useTranslation();
  const left = data.queue.reduce(
    (n, x) => n + Math.max(0, x.size - x.doneBytes),
    0,
  );
  const rate = data.settings.paused ? 0 : data.speed;
  const c = data.cap;
  const tiles: [string, string, string?][] = [
    [
      t("dl.stats.speed"),
      data.settings.paused
        ? t("dl.status.paused")
        : rate > 0
          ? speed(rate)
          : t("dl.idle"),
      data.settings.speedLimit > 0
        ? t("dl.stats.limit", { speed: speed(data.settings.speedLimit * 1024) })
        : undefined,
    ],
    [
      t("dl.stats.queue"),
      left > 0 ? bytes(left) : t("dl.stats.nothing"),
      data.queue.length > 0
        ? t("dl.stats.left", { count: data.queue.length })
        : undefined,
    ],
    [
      t("dl.stats.time"),
      left > 0 && rate > 0 ? duration(left, rate) : "—",
      left > 0 && rate > 0
        ? t("dl.stats.atSpeed")
        : left > 0
          ? t("dl.stats.once")
          : undefined,
    ],
    [
      t("dl.stats.month"),
      bytes(c.used),
      c.enabled && c.limit > 0
        ? t("dl.stats.ofCap", {
            gb: c.gb.toLocaleString(formatLocale()),
            pct: new Intl.NumberFormat(formatLocale(), {
              style: "percent",
            }).format(Math.min(1, c.used / c.limit)),
          })
        : t("dl.stats.noCap"),
    ],
  ];
  return (
    <dl className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
      {tiles.map(([label, value, note]) => (
        <div
          key={label}
          className="rounded-xl border border-line bg-card px-4 py-3"
        >
          <dt className="text-xs text-muted">{label}</dt>
          <dd className="mt-0.5 font-display text-lg font-semibold tabular-nums">
            {value}
          </dd>
          {note && <dd className="text-xs text-muted">{note}</dd>}
        </div>
      ))}
    </dl>
  );
}

// TurnOn says why downloading is off and what's needed to turn it on.
function TurnOn({ data }: { data: DownloadsData }) {
  const { t } = useTranslation();
  return (
    <Card className="mb-6">
      <h2 className="mb-1 font-display text-lg font-semibold">
        {t("dl.turnOn.title")}
      </h2>
      <p className="mb-3 text-sm text-muted">{t("dl.turnOn.sub")}</p>
      <ol className="mb-4 list-decimal space-y-1 ps-5 text-sm">
        <li className={data.newsServers > 0 ? "text-muted line-through" : ""}>
          {t("dl.turnOn.step1")}
        </li>
        <li>{t("dl.turnOn.step2")}</li>
        <li>{t("dl.turnOn.step3")}</li>
      </ol>
      <Link to="/settings#downloads">
        <Button variant="primary">{t("dl.turnOn.open")}</Button>
      </Link>
    </Card>
  );
}

function title(d: Download) {
  const t = titleLink(d.kind, d.titleId);
  if (!t) return null;
  return (
    <Link to={t.to} className="text-xs text-marquee hover:underline">
      {t.label}
    </Link>
  );
}

function useAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      what,
    }: {
      id: number;
      what: "pause" | "resume" | "retry" | "reprocess" | "import" | "top";
    }) => post(`/downloads/${id}/${what}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["downloads"] }),
  });
}

function QueueItem({
  d,
  rate,
  first,
  pick,
}: {
  d: Download;
  rate: number;
  first: boolean;
  pick?: Pick;
}) {
  const { t } = useTranslation();
  const qualities = useQualities();
  const act = useAction();
  const [removing, setRemoving] = useState(false);
  const pct = d.size > 0 ? Math.min(100, (d.doneBytes / d.size) * 100) : 0;
  const q = qualities.data?.find((x) => x.id === d.quality);
  const busy = act.isPending && act.variables?.id === d.id;
  const working = ["verifying", "repairing", "unpacking", "importing"].includes(
    d.status,
  );
  // Checking, repairing and unpacking say how far they've got.
  const step =
    working && d.stepTotal
      ? Math.min(100, ((d.stepDone ?? 0) / d.stepTotal) * 100)
      : undefined;
  return (
    <li
      className={`rounded-xl border bg-card p-4 ${pick?.picked ? "border-marquee" : "border-line"}`}
    >
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        {pick && <PickBox d={d} picked={pick.picked} onToggle={pick.toggle} />}
        <div className="min-w-0 flex-1 basis-60" onClick={pick?.toggle}>
          <div className="break-words font-medium">{d.name}</div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
            <Badge tone={statusTone[d.status]}>{statusName(d.status)}</Badge>
            {q && q.id > 0 && <span>{q.name}</span>}
            {d.indexer && <span>{t("dl.from", { indexer: d.indexer })}</span>}
            {title(d)}
          </div>
        </div>
        <div className={`flex shrink-0 gap-1 ${pick ? "hidden" : ""}`}>
          {(d.status === "queued" || d.status === "downloading") && (
            <IconButton
              label={t("dl.pause")}
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "pause" })}
            >
              <Pause className="size-4" />
            </IconButton>
          )}
          {d.status === "paused" && (
            <IconButton
              label={t("dl.resume")}
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "resume" })}
            >
              <Play className="size-4" />
            </IconButton>
          )}
          {!first && (d.status === "queued" || d.status === "paused") && (
            <IconButton
              label={t("dl.top")}
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "top" })}
            >
              <ArrowUpToLine className="size-4" />
            </IconButton>
          )}
          {!working && (
            <IconButton
              label={t("dl.remove")}
              onClick={() => setRemoving(true)}
            >
              <Trash2 className="size-4" />
            </IconButton>
          )}
        </div>
      </div>
      <div
        className="mt-3 h-2 overflow-hidden rounded-full bg-line"
        role="progressbar"
        aria-valuenow={Math.round(step ?? pct)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={t("dl.progress")}
      >
        <div
          className={`h-full rounded-full transition-all ${working ? "bg-gold" : "bg-marquee"} ${working && step === undefined ? "animate-pulse" : ""}`}
          style={{ width: `${working ? (step ?? 100) : pct}%` }}
        />
      </div>
      <div className="mt-1.5 flex flex-wrap justify-between gap-x-4 text-xs text-muted">
        <span>
          {step !== undefined ? (
            <>
              {statusName(d.status)} {Math.floor(step)}%
              {d.status !== "repairing" &&
                ` · ${t("dl.of", { done: bytes(d.stepDone ?? 0), total: bytes(d.stepTotal ?? 0) })}`}
            </>
          ) : (
            <>
              {t("dl.of", { done: bytes(d.doneBytes), total: bytes(d.size) })}
            </>
          )}
          {d.failedBytes > 0 && (
            <span className="text-bad">
              {" "}
              · {t("dl.missing", { size: bytes(d.failedBytes) })}
            </span>
          )}
        </span>
        <span>
          {d.status === "downloading" && rate > 0
            ? `${speed(rate)} · ${eta(d.size - d.doneBytes, rate)}`
            : d.message}
        </span>
      </div>
      <ErrorNote error={act.error} />
      {removing && (
        <RemoveDialog d={d} queue onClose={() => setRemoving(false)} />
      )}
    </li>
  );
}

function HistoryItem({ d, pick }: { d: Download; pick?: Pick }) {
  const { t } = useTranslation();
  const act = useAction();
  const [removing, setRemoving] = useState(false);
  const ok = d.status === "completed";
  const held = !!d.quarantined;
  const waiting = ok && !!d.canImport;
  // Added by hand to be filed, and left on Import files to check.
  const toCheck = ok && !!d.fileAs && !d.importedTo;
  return (
    <li
      className={`flex flex-wrap items-start gap-3 px-4 py-3 ${pick?.picked ? "bg-marquee/5" : ""}`}
    >
      {pick && <PickBox d={d} picked={pick.picked} onToggle={pick.toggle} />}
      {held ? (
        <ShieldAlert
          className="mt-0.5 size-5 shrink-0 text-gold"
          aria-label={t("dl.quarantined")}
        />
      ) : waiting || toCheck ? (
        <Clock
          className="mt-0.5 size-5 shrink-0 text-gold"
          aria-label={t("dl.waitingLibrary")}
        />
      ) : ok ? (
        <CircleCheck
          className="mt-0.5 size-5 shrink-0 text-ok"
          aria-label={t("dl.status.completed")}
        />
      ) : (
        <CircleX
          className="mt-0.5 size-5 shrink-0 text-bad"
          aria-label={t("dl.status.failed")}
        />
      )}
      <div className="min-w-0 flex-1 basis-56" onClick={pick?.toggle}>
        <div className="break-words text-sm font-medium">
          {d.name} {held && <Badge tone="gold">{t("dl.quarantined")}</Badge>}
        </div>
        <div className="mt-0.5 text-xs text-muted">
          {bytes(d.size)} · {when(d.finishedAt)} {title(d)}
        </div>
        {d.message && (
          <p
            className={`mt-1 break-words text-xs ${ok ? "text-muted" : "text-bad"}`}
          >
            {d.message}
          </p>
        )}
        {toCheck && !pick && (
          <Link
            to="/import-files"
            className="mt-2 inline-flex items-center gap-1 rounded-md border border-line px-2.5 py-1 text-xs font-medium hover:border-ink/40"
          >
            <FolderInput className="size-3.5" aria-hidden />{" "}
            {t("dl.checkImport")}
          </Link>
        )}
        {waiting && !pick && (
          <Button
            className="mt-2 px-2.5 py-1 text-xs"
            busy={act.isPending && act.variables?.what === "import"}
            onClick={() => act.mutate({ id: d.id, what: "import" })}
          >
            <FolderInput className="size-3.5" aria-hidden /> {t("dl.moveIn")}
          </Button>
        )}
        {d.canReprocess && !pick && (
          <Button
            className="mt-2 px-2.5 py-1 text-xs"
            busy={act.isPending && act.variables?.what === "reprocess"}
            onClick={() => act.mutate({ id: d.id, what: "reprocess" })}
          >
            <PackageOpen className="size-3.5" aria-hidden />{" "}
            {t("dl.unpackAgain")}
          </Button>
        )}
      </div>
      <div className={`flex shrink-0 gap-1 ${pick ? "hidden" : ""}`}>
        {!ok && !held && (
          <IconButton
            label={t("dl.again")}
            busy={act.isPending}
            onClick={() => act.mutate({ id: d.id, what: "retry" })}
          >
            <RotateCcw className="size-4" />
          </IconButton>
        )}
        <IconButton label={t("dl.remove")} onClick={() => setRemoving(true)}>
          <Trash2 className="size-4" />
        </IconButton>
      </div>
      {act.error && (
        <div className="basis-full">
          <ErrorNote error={act.error} />
        </div>
      )}
      {removing && <RemoveDialog d={d} onClose={() => setRemoving(false)} />}
    </li>
  );
}

function RemoveDialog({
  d,
  queue,
  onClose,
}: {
  d: Download;
  queue?: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [files, setFiles] = useState(
    !d.quarantined && (d.status !== "completed" || !!d.importedTo),
  );
  // Taking one off the queue usually means it's the wrong release, so it's
  // blocked unless you say otherwise. (A failed one was blocked already.)
  const [block, setBlock] = useState(!!queue);
  const remove = useMutation({
    mutationFn: () => {
      const q = new URLSearchParams();
      if (files) q.set("deleteFiles", "true");
      if (block && d.kind) q.set("block", "true");
      return del(`/downloads/${d.id}${q.toString() ? `?${q}` : ""}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["downloads"] });
      onClose();
    },
  });
  return (
    <Modal title={t("dl.removeTitle")} onClose={onClose}>
      <p className="mb-4 break-words text-sm">{d.name}</p>
      {d.quarantined && (
        <p className="text-sm text-muted">
          <Trans
            i18nKey="dl.staysQuarantined"
            values={{ path: d.quarantined }}
            components={{ code: <code className="break-all" /> }}
          />
        </p>
      )}
      <div className="space-y-3">
        {d.folder && !d.quarantined && (
          <Toggle
            checked={files}
            onChange={setFiles}
            label={
              <>
                {t("dl.alsoDelete")}
                <span className="block text-xs text-muted">
                  {t("dl.neverTouched")}
                </span>
              </>
            }
          />
        )}
        {d.kind && <BlockToggle checked={block} onChange={setBlock} />}
      </div>
      <ErrorNote error={remove.error} />
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>{t("remove.keepIt")}</Button>
        <Button
          variant="danger"
          busy={remove.isPending}
          onClick={() => remove.mutate()}
        >
          {t("dl.remove")}
        </Button>
      </div>
    </Modal>
  );
}

// Search is the box that narrows a list to the downloads whose names
// have what's typed.
function Search({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <Input
      type="search"
      className="order-last sm:order-none sm:w-56"
      placeholder={t("dl.search")}
      aria-label={t("dl.search")}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function IconButton({
  label,
  onClick,
  busy,
  children,
}: {
  label: string;
  onClick: () => void;
  busy?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={busy}
      title={label}
      aria-label={label}
      className="rounded-md p-2 text-muted hover:bg-line hover:text-ink disabled:opacity-50"
    >
      {children}
    </button>
  );
}
