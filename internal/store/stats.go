package store

import "context"

// LibraryStats counts what's in the library, for dashboards.
type LibraryStats struct {
	Movies          int `json:"movies"`
	MoviesOnDisk    int `json:"moviesOnDisk"`
	MoviesMissing   int `json:"moviesMissing"` // wanted, released, no file
	Series          int `json:"series"`
	Episodes        int `json:"episodes"` // aired
	EpisodesOnDisk  int `json:"episodesOnDisk"`
	EpisodesMissing int `json:"episodesMissing"` // wanted (its season too), aired, no file
}

// LibraryStats counts movies, shows and episodes, with and without files.
// Missing counts only what's wanted (monitored) and already out.
func (s *Store) LibraryStats(ctx context.Context) (LibraryStats, error) {
	var st LibraryStats
	t := now()
	err := s.row(ctx, `SELECT
		(SELECT COUNT(*) FROM movies),
		(SELECT COUNT(*) FROM movies m WHERE EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'movie' AND f.owner_id = m.id)),
		(SELECT COUNT(*) FROM movies m WHERE m.monitored = 1
			AND COALESCE(m.digital_release, m.physical_release, m.in_cinemas, 0) BETWEEN 1 AND ?
			AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'movie' AND f.owner_id = m.id)),
		(SELECT COUNT(*) FROM series),
		(SELECT COUNT(*) FROM episodes WHERE air_date IS NOT NULL AND air_date <= ?),
		(SELECT COUNT(*) FROM episodes e WHERE EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'episode' AND f.owner_id = e.id)),
		(SELECT COUNT(*) FROM episodes e JOIN seasons se ON se.series_id = e.series_id AND se.number = e.season
			WHERE e.monitored = 1 AND se.monitored = 1 AND e.air_date IS NOT NULL AND e.air_date <= ?
			AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.kind = 'episode' AND f.owner_id = e.id))`,
		t, t, t).Scan(&st.Movies, &st.MoviesOnDisk, &st.MoviesMissing, &st.Series, &st.Episodes, &st.EpisodesOnDisk, &st.EpisodesMissing)
	return st, err
}
