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
		MaxOpenConns:           getEnvInt(s.maxOpen.key, s.maxOpen.fallback),
		MaxIdleConns:           getEnvInt(s.maxIdle.key, s.maxIdle.fallback),
		ConnMaxLifetimeSeconds: getEnvInt(s.connMaxLifetime.key, s.connMaxLifetime.fallback),
		ConnMaxIdleTimeSeconds: getEnvInt(s.connMaxIdleTime.key, s.connMaxIdleTime.fallback),
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
		ClientID:     getEnv(s.clientID.key, s.clientID.fallback),
		ClientSecret: getEnv(s.clientSecret.key, s.clientSecret.fallback),
		RedirectURI:  getEnv(s.redirectURI.key, s.redirectURI.fallback),
	}
}
