import { describe, expect, it } from "vitest";
import type { Download } from "../api";
import { filterQueue, queueState, typeOf } from "./DownloadsFilter";

const dl = (id: number, status: string, kind?: string, fileAs?: string) =>
  ({ id, name: `d${id}`, status, kind, fileAs }) as Download;

const list = [
  dl(1, "downloading", "series"),
  dl(2, "queued", "series"),
  dl(3, "queued", "movie"),
  dl(4, "downloaded", "series"),
  dl(5, "unpacking", "movie"),
  dl(6, "verifying", "album"),
  dl(7, "queued", undefined, "game"),
  dl(8, "paused", undefined, "auto"),
];

describe("queue filters", () => {
  it("works out a download's type and state", () => {
    expect(list.map(typeOf)).toEqual([
      "series",
      "series",
      "movie",
      "series",
      "movie",
      "album",
      "game",
      "other",
    ]);
    expect(list.map(queueState)).toEqual([
      "downloading",
      "queued",
      "queued",
      "downloaded",
      "working",
      "working",
      "queued",
      "paused",
    ]);
  });

  it("keeps everything with no filter", () => {
    const f = filterQueue(list, "", "");
    expect(f.shown).toHaveLength(8);
    expect(f.types).toEqual({
      series: 3,
      movie: 2,
      album: 1,
      game: 1,
      other: 1,
    });
    expect(f.states).toEqual({
      downloading: 1,
      queued: 3,
      downloaded: 1,
      working: 2,
      paused: 1,
    });
  });

  it("counts each filter with the other applied", () => {
    const f = filterQueue(list, "series", "queued");
    expect(f.shown.map((d) => d.id)).toEqual([2]);
    // Queued downloads of every type, and shows in every state.
    expect(f.types).toEqual({ series: 1, movie: 1, game: 1 });
    expect(f.states).toEqual({ downloading: 1, queued: 1, downloaded: 1 });
  });

  it("puts checking, unpacking and moving together", () => {
    expect(filterQueue(list, "", "working").shown.map((d) => d.id)).toEqual([
      5, 6,
    ]);
  });
});
