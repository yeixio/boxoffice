// Adding games: finding them on each system's list (No-Intro's for
// consoles, MAME's for arcade games), wanting them, and following the
// search for and download of each one.
import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  Check,
  ChevronRight,
  Gamepad2,
  ListFilter,
  Search,
  X,
} from "lucide-react";
import i18n from "../i18n";
import {
  del,
  get,
  post,
  type CatalogGame,
  type GameLookup,
  type GameSystem,
  type GameSystemInfo,
  type GameWant,
} from "../api";
import { bytes, when } from "../format";
import { Releases } from "../Releases";
import {
  Badge,
  Button,
  Card,
  ErrorNote,
  Heading,
  Input,
  Select,
  Spinner,
} from "../ui";
import { SystemIcon } from "./Games";

function useAllSystems() {
  return useQuery({
    queryKey: ["game-systems-all"],
    queryFn: () => get<GameSystemInfo[]>("/games/systems"),
    staleTime: Infinity,
  });
}

export function useGameWants() {
  return useQuery({
    queryKey: ["game-wants"],
    queryFn: () => get<GameWant[]>("/gamewants"),
    refetchInterval: (q) =>
      q.state.data?.some((w) => w.download || w.gotAt) ? 5000 : 60_000,
  });
}

// Picture is a game's picture, or its name over its system's where there's
// none.
function Picture({
  src,
  title,
  system,
  short,
  small,
  className = "",
}: {
  src: string;
  title: string;
  system: string;
  short: string;
  small?: boolean;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  return (
    <div
      className={`relative aspect-[3/4] overflow-hidden rounded-lg bg-line ${className}`}
    >
      {!failed ? (
        <img
          src={src}
          alt=""
          loading="lazy"
          className="size-full object-contain"
          onError={() => setFailed(true)}
        />
      ) : (
        <div className="flex size-full flex-col items-center justify-center gap-2 p-2 text-center font-display text-xs text-muted">
          <SystemIcon
            system={system}
            short={short}
            className={small ? "w-4/5 opacity-60" : "w-3/5 opacity-60"}
          />
          {!small && (
            <span className="line-clamp-3 font-semibold text-ink/80">
              {title}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

const lookupArt = (g: CatalogGame) =>
  `/api/v1/games/lookup/art?${new URLSearchParams({ system: g.system, name: g.name })}`;

// versionLabel says which version a game on a list is: its regions and
// revision for a console, its set name and title for MAME.
function versionLabel(g: CatalogGame, arcade: boolean) {
  if (arcade) return `${g.title} (${g.name})`;
  return g.tags?.length ? g.tags.join(" · ") : g.name;
}

// AddGame finds games on the systems' lists, to want.
export function AddGame() {
  const { t } = useTranslation();
  const [term, setTerm] = useState("");
  const [q, setQ] = useState("");
  const [system, setSystem] = useState("");
  const all = useAllSystems();
  const mine = useQuery({
    queryKey: ["game-systems"],
    queryFn: () => get<GameSystem[]>("/gamesystems"),
  });
  useEffect(() => {
    const timer = setTimeout(() => setQ(term.trim()), 400);
    return () => clearTimeout(timer);
  }, [term]);
  const params = new URLSearchParams({ q });
  if (system) params.set("system", system);
  const results = useQuery({
    queryKey: ["game-lookup", q, system],
    queryFn: () => get<GameLookup[]>(`/games/lookup?${params}`),
    enabled: q.length > 1,
    staleTime: 60_000,
  });
  const have = new Set(mine.data?.map((s) => s.system));
  const options = [
    {
      value: "",
      label: have.size ? t("games.add.yourSystems") : t("games.add.allSystems"),
    },
    ...(all.data ?? []).map((s) => ({
      value: s.key,
      label:
        have.has(s.key) || !have.size
          ? s.name
          : t("games.add.notYours", { name: s.name }),
    })),
  ];
  const info = useMemo(
    () => new Map((all.data ?? []).map((s) => [s.key, s])),
    [all.data],
  );
  return (
    <>
      <nav className="mb-2 text-sm text-muted">
        <Link to="/games" className="hover:text-ink">
          {t("nav.games")}
        </Link>
      </nav>
      <Heading title={t("games.add.title")} sub={t("games.add.sub")} />
      <div className="mb-6 flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <Search
            className="pointer-events-none absolute start-3 top-2.5 size-4 text-muted"
            aria-hidden
          />
          <Input
            autoFocus
            type="search"
            className="ps-9"
            placeholder={t("games.add.placeholder")}
            aria-label={t("games.add.placeholder")}
            value={term}
            onChange={(e) => setTerm(e.target.value)}
          />
        </div>
        <div className="sm:w-72">
          <Select value={system} onChange={setSystem} options={options} />
        </div>
      </div>
      {results.isFetching && <Spinner label={t("games.add.looking")} />}
      <ErrorNote error={results.error} />
      {q.length < 2 && !results.data && (
        <div className="rounded-xl border border-dashed border-line px-6 py-12 text-center text-sm text-muted">
          <Gamepad2 className="mx-auto mb-3 size-8" aria-hidden />
          <p className="mx-auto max-w-md">{t("games.add.hint")}</p>
        </div>
      )}
      {results.data?.length === 0 && !results.isFetching && (
        <p className="py-10 text-center text-sm text-muted">
          {t("games.add.nothing")}
        </p>
      )}
      {!!results.data?.length && (
        <ul className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
          {results.data.map((l) => (
            <LookupCard
              key={`${l.system}:${l.best.name}`}
              found={l}
              system={info.get(l.system)}
            />
          ))}
        </ul>
      )}
      {results.data?.length === 60 && (
        <p className="mt-6 text-center text-sm text-muted">
          {t("games.add.more")}
        </p>
      )}
    </>
  );
}

function LookupCard({
  found: l,
  system,
}: {
  found: GameLookup;
  system?: GameSystemInfo;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const arcade = !!system?.arcade;
  const [name, setName] = useState(l.wantName ?? l.best.name);
  const picked = l.versions.find((v) => v.name === name) ?? l.best;
  const want = useMutation({
    mutationFn: () =>
      post<GameWant>("/gamewants", { system: l.system, name: picked.name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["game-wants"] });
      qc.invalidateQueries({ queryKey: ["game-lookup"] });
    },
  });
  const wanted = !!l.wantId || want.isSuccess;
  return (
    <li className="flex flex-col">
      <Picture
        src={lookupArt(picked)}
        title={l.title}
        system={l.system}
        short={system?.short ?? l.system}
      />
      <div className="mt-2 line-clamp-2 text-sm font-medium leading-snug">
        {l.title}
      </div>
      <div className="truncate text-xs text-muted">
        {[picked.year, picked.maker].filter(Boolean).join(" · ") || " "}
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-1">
        <Badge>{system?.short ?? l.system}</Badge>
        {!!picked.size && !arcade && (
          <span className="text-xs text-muted">{bytes(picked.size)}</span>
        )}
      </div>
      {l.versions.length > 1 && !wanted && !l.onDisk && (
        <label className="mt-2 block text-xs text-muted">
          <span className="sr-only">{t("games.add.version")}</span>
          <select
            value={name}
            onChange={(e) => setName(e.target.value)}
            aria-label={t("games.add.versionOf", { title: l.title })}
            className="w-full rounded-md border border-line bg-paper px-2 py-1 text-xs text-ink"
          >
            {l.versions.map((v) => (
              <option key={v.name} value={v.name}>
                {versionLabel(v, arcade)}
              </option>
            ))}
          </select>
        </label>
      )}
      <div className="mt-2">
        {l.onDisk ? (
          <Badge tone="ok">
            <Check className="size-3" aria-hidden /> {t("games.add.have")}
          </Badge>
        ) : wanted ? (
          <Link to="/games#wanted" className="inline-flex">
            <Badge tone="gold">{t("games.add.wanted")}</Badge>
          </Link>
        ) : (
          <Button
            className="w-full justify-center"
            busy={want.isPending}
            onClick={() => want.mutate()}
          >
            {t("games.add.want")}
          </Button>
        )}
      </div>
      {want.error instanceof Error && (
        <p className="mt-1 text-xs text-bad">{want.error.message}</p>
      )}
    </li>
  );
}

const downloadTone: Record<string, "gold" | "plain" | "ok" | "bad"> = {
  queued: "plain",
  paused: "plain",
  downloading: "gold",
  downloaded: "gold",
  verifying: "gold",
  repairing: "gold",
  unpacking: "gold",
  importing: "gold",
  completed: "ok",
  failed: "bad",
};

// wantState says how the looking for a game wanted is going.
function wantState(w: GameWant): string {
  if (w.gotAt) return i18n.t("web:games.wanted.movedIn");
  if (w.download?.status === "completed")
    return i18n.t("web:games.wanted.waiting");
  if (w.download) return "";
  if (!w.searched) return i18n.t("web:games.wanted.notYet");
  if (w.misses)
    return i18n.t("web:games.wanted.nothing", { when: when(w.searched) });
  return i18n.t("web:games.wanted.looked", { when: when(w.searched) });
}

// WantedGames lists the games wanted, on the Games page.
export function WantedGames() {
  const { t } = useTranslation();
  const wants = useGameWants();
  const [releasesFor, setReleasesFor] = useState<GameWant>();
  if (!wants.data?.length) return null;
  return (
    <section id="wanted" className="mt-10 scroll-mt-20">
      <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 className="font-display text-xl font-semibold">
            {t("games.wanted.title")}{" "}
            <span className="font-normal text-muted">{wants.data.length}</span>
          </h2>
          <p className="text-sm text-muted">{t("games.wanted.sub")}</p>
        </div>
        <Link to="/games/add" className="text-sm font-medium text-marquee">
          {t("games.add.title")}
        </Link>
      </div>
      <ul className="flex flex-col divide-y divide-line rounded-xl border border-line bg-card">
        {wants.data.map((w) => (
          <WantRow key={w.id} want={w} onReleases={() => setReleasesFor(w)} />
        ))}
      </ul>
      {releasesFor && (
        <Releases
          path={`/gamewants/${releasesFor.id}/releases`}
          title={`${releasesFor.title} (${releasesFor.systemShort})`}
          onClose={() => setReleasesFor(undefined)}
        />
      )}
    </section>
  );
}

function WantRow({
  want: w,
  onReleases,
}: {
  want: GameWant;
  onReleases: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const search = useMutation({
    mutationFn: () => post(`/gamewants/${w.id}/search`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["jobs"] }),
  });
  const remove = useMutation({
    mutationFn: () => del(`/gamewants/${w.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["game-wants"] });
      qc.invalidateQueries({ queryKey: ["game-lookup"] });
    },
  });
  const d = w.download;
  const state = wantState(w);
  const pct =
    d && d.status === "downloading" && d.size > 0
      ? Math.floor((d.doneBytes / d.size) * 100)
      : undefined;
  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-3">
      <Picture
        src={`/api/v1/gamewants/${w.id}/art`}
        title={w.title}
        system={w.system}
        short={w.systemShort}
        small
        className="w-12 shrink-0"
      />
      <div className="min-w-0 flex-1 basis-48">
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1 truncate font-medium">{w.title}</div>
          <Button
            variant="ghost"
            className="-my-1.5 -me-2 !px-2 !py-1"
            busy={remove.isPending}
            onClick={() => remove.mutate()}
            aria-label={t("games.wanted.remove", { title: w.title })}
            title={t("games.wanted.remove", { title: w.title })}
          >
            <X className="size-4" aria-hidden />
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
          <Badge>{w.systemShort}</Badge>
          <code className="truncate">{w.name}</code>
        </div>
        <div
          className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted"
          role="status"
        >
          {d && d.status !== "completed" && (
            <Badge tone={downloadTone[d.status] ?? "plain"}>
              {t(`dl.status.${d.status}`, { defaultValue: d.status })}
              {pct !== undefined && ` ${pct}%`}
            </Badge>
          )}
          {d && <span className="truncate">{d.name}</span>}
          {state && <span>{state}</span>}
          {search.isSuccess && (
            <span className="text-ink">{t("games.wanted.lookingNow")}</span>
          )}
        </div>
        {(search.error ?? remove.error) instanceof Error && (
          <p className="mt-1 text-xs text-bad">
            {((search.error ?? remove.error) as Error).message}
          </p>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          busy={search.isPending}
          disabled={search.isSuccess || !!d}
          onClick={() => search.mutate()}
        >
          <Search className="size-4" aria-hidden /> {t("books.lookFor")}
        </Button>
        <Button onClick={onReleases}>
          <ListFilter className="size-4" aria-hidden /> {t("title.choose")}
        </Button>
      </div>
    </li>
  );
}

// AddGameCard points to Add a game from the Games page when no game is
// wanted yet.
export function AddGameCard() {
  const { t } = useTranslation();
  return (
    <Card className="mt-10">
      <Link to="/games/add" className="group flex items-center gap-4">
        <Search className="size-6 shrink-0 text-marquee" aria-hidden />
        <span className="min-w-0 flex-1">
          <span className="block font-display text-lg font-semibold group-hover:text-marquee">
            {t("games.add.cardTitle")}
          </span>
          <span className="block text-sm text-muted">
            {t("games.add.cardSub")}
          </span>
        </span>
        <ChevronRight
          className="size-4 shrink-0 text-muted rtl:rotate-180"
          aria-hidden
        />
      </Link>
    </Card>
  );
}
