import { useEffect, useRef, useState } from "react";
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
  Pause,
  Play,
  PackageOpen,
  RotateCcw,
  ShieldAlert,
  Trash2,
  Upload,
} from "lucide-react";
import {
  api,
  del,
  get,
  post,
  put,
  type Download,
  type Downloads as DownloadsData,
} from "../api";
import { bytes, when } from "../format";
import { useQualities } from "../hooks";
import { SortControl, useSort, words, type SortOption } from "../sort";
import {
  Badge,
  Button,
  Card,
  Empty,
  ErrorNote,
  Heading,
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

// HistoryPage is which page of the history to fetch, sorted how; the
// server pages and sorts it, as it can grow long.
type HistoryPage = {
  offset: number;
  limit: number;
  sort: string;
  dir: "asc" | "desc";
};

export function useDownloads(page?: HistoryPage) {
  const q = page
    ? `?historyOffset=${page.offset}&historyLimit=${page.limit}&historySort=${page.sort}&historyDir=${page.dir}`
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

const statusName: Record<string, string> = {
  queued: "Waiting",
  downloading: "Downloading",
  paused: "Paused",
  downloaded: "Waiting to unpack",
  verifying: "Checking",
  repairing: "Repairing",
  unpacking: "Unpacking",
  importing: "Moving to library",
  completed: "Done",
  failed: "Failed",
};

export function speed(n: number) {
  return `${bytes(n)}/s`;
}

function eta(left: number, rate: number) {
  if (rate <= 0 || left <= 0) return "";
  const s = left / rate;
  if (s < 60) return "under a minute left";
  if (s < 3600) return `${Math.round(s / 60)} min left`;
  const h = Math.floor(s / 3600);
  if (h >= 48) return `${Math.floor(h / 24)} d ${h % 24} h left`;
  return `${h} h ${Math.round((s % 3600) / 60)} min left`;
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
    label: "Date finished",
    value: (d) => d.finishedAt ?? d.addedAt,
    dir: "desc",
    says: words.date,
  },
  {
    key: "added",
    label: "Date added",
    value: (d) => d.addedAt,
    dir: "desc",
    says: words.date,
  },
  {
    key: "name",
    label: "Name",
    value: (d) => d.name,
    dir: "asc",
    says: words.text,
  },
  {
    key: "size",
    label: "Size",
    value: (d) => d.size || undefined,
    dir: "desc",
    says: words.size,
  },
  {
    key: "status",
    label: "Status",
    value: outcome,
    dir: "asc",
    says: { asc: "Problems first", desc: "Done first" },
  },
];

export function Downloads() {
  const qc = useQueryClient();
  const sort = useSort("downloads-history", historySorts);
  // A few rows at a time on a phone, more on a bigger screen.
  const queueSize = usePageSize([4, 6, 8]);
  const historySize = usePageSize([6, 10, 15]);
  const [queuePage, setQueuePage] = useState(0);
  const [historyPage, setHistoryPage] = useState(0);
  useEffect(() => setHistoryPage(0), [sort.option.key, sort.dir, historySize]);
  const d = useDownloads({
    offset: historyPage * historySize,
    limit: historySize,
    sort: sort.option.key,
    dir: sort.dir,
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
  const file = useRef<HTMLInputElement>(null);
  const upload = useMutation({
    mutationFn: async (f: File) =>
      api("POST", `/downloads?name=${encodeURIComponent(f.name)}`, f),
    onSuccess: refresh,
  });
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
  // The queue's own pages; a page past the end (the queue got shorter)
  // shows the last one.
  const queuePages = Math.max(1, Math.ceil(data.queue.length / queueSize));
  const qp = Math.min(queuePage, queuePages - 1);
  const queue = data.queue.slice(qp * queueSize, (qp + 1) * queueSize);
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
          Done
        </Button>
      </>
    ) : (
      <Button variant="ghost" onClick={() => startPicking(list)}>
        <ListChecks className="size-4" aria-hidden /> Select
      </Button>
    ));
  return (
    <div className={picking && picked.size > 0 ? "pb-44 sm:pb-28" : ""}>
      <Heading
        title="Downloads"
        sub={
          s.enabled ? (
            s.paused ? (
              "Everything is paused."
            ) : (
              <>
                {data.speed > 0
                  ? speed(data.speed)
                  : data.waiting && data.queue.length > 0
                    ? `Waiting: ${data.waiting}`
                    : "Idle"}
                {data.queue.length > 0 &&
                  ` · ${data.queue.length} in the queue`}
              </>
            )
          ) : (
            "Downloading is off."
          )
        }
        actions={
          <>
            <input
              ref={file}
              type="file"
              accept=".nzb,application/x-nzb"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) upload.mutate(f);
                e.target.value = "";
              }}
            />
            <Button
              busy={upload.isPending}
              onClick={() => file.current?.click()}
            >
              <Upload className="size-4" aria-hidden /> Add NZB
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
                {s.paused ? "Resume all" : "Pause all"}
              </Button>
            )}
          </>
        }
      />
      {s.enabled && <Stats data={data} />}
      <ErrorNote error={upload.error || settings.error} />
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
                {x.name}: {x.error}. Check it in{" "}
                <Link to="/settings#news-servers" className="underline">
                  Settings
                </Link>
                .
              </p>
            ))}
        </div>
      )}

      <section className="mb-8">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="mr-auto font-display text-xl font-semibold">Queue</h2>
          {pickHead("queue", queue, queuePages > 1)}
        </div>
        {data.queue.length === 0 ? (
          <Empty
            icon={<DownloadIcon className="size-8" />}
            title="Nothing to download"
          >
            Releases Box Office grabs show up here. You can also add an NZB file
            yourself.
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
          total={data.queue.length}
          onPage={turn(setQueuePage)}
          label="Queue"
        />
      </section>

      <section>
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="mr-auto font-display text-xl font-semibold">
            History
          </h2>
          {historyTotal > 1 && (
            <SortControl
              sort={sort}
              className="order-last w-full sm:order-none sm:w-auto"
            />
          )}
          {pickHead("history", history, historyTotal > historySize)}
          {data.history.length > 0 && picking !== "history" && (
            <Button
              variant="ghost"
              busy={clear.isPending}
              onClick={() => clear.mutate()}
            >
              Clear history
            </Button>
          )}
        </div>
        {data.history.length === 0 ? (
          <p className="text-sm text-muted">Finished downloads show up here.</p>
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
          label="History"
        />
      </section>
      <Quarantine />
      <DownloadFiles />
      {data.folder.path && (
        <p className="mt-6 text-xs text-muted">
          Downloads go to {data.folder.path}
          {data.folder.freeSpace > 0 &&
            `, with ${bytes(data.folder.freeSpace)} free`}
          .
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
  const left = data.queue.reduce(
    (n, x) => n + Math.max(0, x.size - x.doneBytes),
    0,
  );
  const rate = data.settings.paused ? 0 : data.speed;
  const c = data.cap;
  const tiles: [string, string, string?][] = [
    [
      "Speed",
      data.settings.paused ? "Paused" : rate > 0 ? speed(rate) : "Idle",
      data.settings.speedLimit > 0
        ? `Limit ${speed(data.settings.speedLimit * 1024)}`
        : undefined,
    ],
    [
      "In the queue",
      left > 0 ? bytes(left) : "Nothing",
      data.queue.length > 0
        ? `${data.queue.length} download${data.queue.length === 1 ? "" : "s"} left`
        : undefined,
    ],
    [
      "Time to finish",
      left > 0 && rate > 0 ? eta(left, rate).replace(" left", "") : "—",
      left > 0 && rate > 0
        ? "at the current speed"
        : left > 0
          ? "once it downloads"
          : undefined,
    ],
    [
      "This month",
      bytes(c.used),
      c.enabled && c.limit > 0
        ? `of ${c.gb.toLocaleString()} GB (${Math.min(100, Math.round((c.used / c.limit) * 100))}%)`
        : "no data cap set",
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
  return (
    <Card className="mb-6">
      <h2 className="mb-1 font-display text-lg font-semibold">
        Downloading is off
      </h2>
      <p className="mb-3 text-sm text-muted">
        Box Office downloads from Usenet itself, in place of NZBGet. It's off
        until you turn it on, so it never competes with the apps you're moving
        from. Searches still run, and Activity says what they found.
      </p>
      <ol className="mb-4 list-decimal space-y-1 pl-5 text-sm">
        <li className={data.newsServers > 0 ? "text-muted line-through" : ""}>
          Add your Usenet provider under News servers in Settings.
        </li>
        <li>Check your indexers in Settings; each needs its API key.</li>
        <li>Turn on downloading in Settings, under Downloads.</li>
      </ol>
      <Link to="/settings#downloads">
        <Button variant="primary">Open Settings</Button>
      </Link>
    </Card>
  );
}

function title(d: Download) {
  if (!d.kind || !d.titleId) return null;
  const to =
    d.kind === "movie" ? `/movies/${d.titleId}` : `/shows/${d.titleId}`;
  return (
    <Link to={to} className="text-xs text-marquee hover:underline">
      {d.kind === "movie" ? "Movie" : "Show"}
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
            <Badge tone={statusTone[d.status]}>
              {statusName[d.status] ?? d.status}
            </Badge>
            {q && q.id > 0 && <span>{q.name}</span>}
            {d.indexer && <span>from {d.indexer}</span>}
            {title(d)}
          </div>
        </div>
        <div className={`flex shrink-0 gap-1 ${pick ? "hidden" : ""}`}>
          {(d.status === "queued" || d.status === "downloading") && (
            <IconButton
              label="Pause"
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "pause" })}
            >
              <Pause className="size-4" />
            </IconButton>
          )}
          {d.status === "paused" && (
            <IconButton
              label="Resume"
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "resume" })}
            >
              <Play className="size-4" />
            </IconButton>
          )}
          {!first && (d.status === "queued" || d.status === "paused") && (
            <IconButton
              label="Move to the top"
              busy={busy}
              onClick={() => act.mutate({ id: d.id, what: "top" })}
            >
              <ArrowUpToLine className="size-4" />
            </IconButton>
          )}
          {!working && (
            <IconButton label="Remove" onClick={() => setRemoving(true)}>
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
        aria-label="Progress"
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
              {statusName[d.status]} {Math.floor(step)}%
              {d.status !== "repairing" &&
                ` · ${bytes(d.stepDone ?? 0)} of ${bytes(d.stepTotal ?? 0)}`}
            </>
          ) : (
            <>
              {bytes(d.doneBytes)} of {bytes(d.size)}
            </>
          )}
          {d.failedBytes > 0 && (
            <span className="text-bad"> · {bytes(d.failedBytes)} missing</span>
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
  const act = useAction();
  const [removing, setRemoving] = useState(false);
  const ok = d.status === "completed";
  const held = !!d.quarantined;
  const waiting = ok && !!d.canImport;
  return (
    <li
      className={`flex flex-wrap items-start gap-3 px-4 py-3 ${pick?.picked ? "bg-marquee/5" : ""}`}
    >
      {pick && <PickBox d={d} picked={pick.picked} onToggle={pick.toggle} />}
      {held ? (
        <ShieldAlert
          className="mt-0.5 size-5 shrink-0 text-gold"
          aria-label="Quarantined"
        />
      ) : waiting ? (
        <Clock
          className="mt-0.5 size-5 shrink-0 text-gold"
          aria-label="Waiting to go into the library"
        />
      ) : ok ? (
        <CircleCheck
          className="mt-0.5 size-5 shrink-0 text-ok"
          aria-label="Done"
        />
      ) : (
        <CircleX
          className="mt-0.5 size-5 shrink-0 text-bad"
          aria-label="Failed"
        />
      )}
      <div className="min-w-0 flex-1 basis-56" onClick={pick?.toggle}>
        <div className="break-words text-sm font-medium">
          {d.name} {held && <Badge tone="gold">Quarantined</Badge>}
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
        {waiting && !pick && (
          <Button
            className="mt-2 px-2.5 py-1 text-xs"
            busy={act.isPending && act.variables?.what === "import"}
            onClick={() => act.mutate({ id: d.id, what: "import" })}
          >
            <FolderInput className="size-3.5" aria-hidden /> Move into library
          </Button>
        )}
        {d.canReprocess && !pick && (
          <Button
            className="mt-2 px-2.5 py-1 text-xs"
            busy={act.isPending && act.variables?.what === "reprocess"}
            onClick={() => act.mutate({ id: d.id, what: "reprocess" })}
          >
            <PackageOpen className="size-3.5" aria-hidden /> Unpack again,
            without downloading
          </Button>
        )}
      </div>
      <div className={`flex shrink-0 gap-1 ${pick ? "hidden" : ""}`}>
        {!ok && !held && (
          <IconButton
            label="Download again"
            busy={act.isPending}
            onClick={() => act.mutate({ id: d.id, what: "retry" })}
          >
            <RotateCcw className="size-4" />
          </IconButton>
        )}
        <IconButton label="Remove" onClick={() => setRemoving(true)}>
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
    <Modal title="Remove download" onClose={onClose}>
      <p className="mb-4 break-words text-sm">{d.name}</p>
      {d.quarantined && (
        <p className="text-sm text-muted">
          Its files stay in quarantine at{" "}
          <code className="break-all">{d.quarantined}</code>; delete or release
          them under Quarantine below. Don't open them.
        </p>
      )}
      <div className="space-y-3">
        {d.folder && !d.quarantined && (
          <Toggle
            checked={files}
            onChange={setFiles}
            label={
              <>
                Also delete its files in the downloads folder
                <span className="block text-xs text-muted">
                  Files already moved into your library are never touched.
                </span>
              </>
            }
          />
        )}
        {d.kind && <BlockToggle checked={block} onChange={setBlock} />}
      </div>
      <ErrorNote error={remove.error} />
      <div className="mt-5 flex justify-end gap-2">
        <Button onClick={onClose}>Keep it</Button>
        <Button
          variant="danger"
          busy={remove.isPending}
          onClick={() => remove.mutate()}
        >
          Remove
        </Button>
      </div>
    </Modal>
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
