package config

// envIntFieldSpec describes one integer environment field and its loader
// fallback. Keeping the key and fallback together prevents repeated mapping
// literals from drifting between equivalent configuration profiles.
type envIntFieldSpec struct {
	key      string
	fallback int
}

// envStringFieldSpec describes one string environment field and its loader
// fallback.
type envStringFieldSpec struct {
	key      string
	fallback string
}

func (s envIntFieldSpec) resolve() int {
	return getEnvInt(s.key, s.fallback)
}

func (s envStringFieldSpec) resolve() string {
	return getEnv(s.key, s.fallback)
}

type dbPoolFieldSpec struct {
	maxOpen         envIntFieldSpec
	maxIdle         envIntFieldSpec
	connMaxLifetime envIntFieldSpec
	connMaxIdleTime envIntFieldSpec
}

func newDBPoolFieldSpec(prefix string, defaults DBPoolProfile) dbPoolFieldSpec {
	return dbPoolFieldSpec{
		maxOpen:         envIntFieldSpec{key: prefix + "_MAX_OPEN_CONNS", fallback: defaults.MaxOpenConns},
		maxIdle:         envIntFieldSpec{key: prefix + "_MAX_IDLE_CONNS", fallback: defaults.MaxIdleConns},
		connMaxLifetime: envIntFieldSpec{key: prefix + "_CONN_MAX_LIFETIME_SECONDS", fallback: defaults.ConnMaxLifetimeSeconds},
		connMaxIdleTime: envIntFieldSpec{key: prefix + "_CONN_MAX_IDLE_TIME_SECONDS", fallback: defaults.ConnMaxIdleTimeSeconds},
	}
}

func (s dbPoolFieldSpec) resolve() DBPoolProfile {
	return DBPoolProfile{
		MaxOpenConns:           s.maxOpen.resolve(),
		MaxIdleConns:           s.maxIdle.resolve(),
		ConnMaxLifetimeSeconds: s.connMaxLifetime.resolve(),
		ConnMaxIdleTimeSeconds: s.connMaxIdleTime.resolve(),
	}
}

type youTubeOAuthClientFieldSpec struct {
	clientID     envStringFieldSpec
	clientSecret envStringFieldSpec
	redirectURI  envStringFieldSpec
}

func newYouTubeOAuthClientFieldSpec(slot string) youTubeOAuthClientFieldSpec {
	prefix := "YOUTUBE_OAUTH_CLIENT_" + slot
	return youTubeOAuthClientFieldSpec{
		clientID:     envStringFieldSpec{key: prefix + "_ID", fallback: ""},
		clientSecret: envStringFieldSpec{key: prefix + "_SECRET", fallback: ""},
		redirectURI:  envStringFieldSpec{key: prefix + "_REDIRECT_URI", fallback: ""},
	}
}

func (s youTubeOAuthClientFieldSpec) resolve() YouTubeOAuthPoolClient {
	return YouTubeOAuthPoolClient{
		ClientID:     s.clientID.resolve(),
		ClientSecret: s.clientSecret.resolve(),
		RedirectURI:  s.redirectURI.resolve(),
	}
}
