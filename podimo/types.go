package podimo

// Podcast represents the podcast metadata returned by the GraphQL
// ChannelEpisodesQuery.
type Podcast struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	WebAddress  string `json:"webAddress"`
	AuthorName  string `json:"authorName"`
	Language    string `json:"language"`
	Images      struct {
		CoverImageURL string `json:"coverImageUrl"`
	} `json:"images"`
}

// EpisodeAudio represents the audio/media attachment of an episode.
type EpisodeAudio struct {
	URL      string  `json:"url"`
	Duration float64 `json:"duration"`
}

// Episode represents a single podcast episode.
type Episode struct {
	ID              string       `json:"id"`
	Artist          string       `json:"artist"`
	PodcastName     string       `json:"podcastName"`
	ImageURL        string       `json:"imageUrl"`
	Description     string       `json:"description"`
	Datetime        string       `json:"datetime"`
	PublishDatetime string       `json:"publishDatetime"`
	Title           string       `json:"title"`
	Audio           EpisodeAudio `json:"audio"`
	StreamMedia     EpisodeAudio `json:"streamMedia"`
}

// PodcastData is the top-level shape returned by ChannelEpisodesQuery: the
// podcast metadata plus the aggregated episode list.
type PodcastData struct {
	Podcast  Podcast   `json:"podcast"`
	Episodes []Episode `json:"episodes"`
}

// SearchResult represents a single entry from podcastsAutocomplete / searchPodcasts.
type SearchResult struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CoverImageURL string `json:"coverImageUrl"`
	AuthorName    string `json:"authorName"`
	Description   string `json:"description"`
}

// FollowedPodcast represents a single entry from podcastsFollowed.
type FollowedPodcast struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	CoverImageURL string        `json:"coverImageUrl"`
	EpisodeCount  int           `json:"episodeCount"`
	LatestEpisode latestEpisode `json:"latestEpisode"`
}

// latestEpisode is the nested object Podimo returns on podcastsFollowed;
// we only need the publish date, so only that field is decoded.
type latestEpisode struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	PublishDatetime string `json:"publishDatetime"`
}
