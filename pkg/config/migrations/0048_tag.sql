-- 0048: the tag registry (pkg/config/kind.go: Kinds, "tag"; docs/SPEC-CONFIG.md,
-- "The tag registry"; tla/Tags.tla). A tag is a word a card may require on its
-- TAGS: line and a machine may carry. It is declared by a machine row or proved
-- by the member's heartbeat running this row's one probe. A new tag is a row
-- here and an apply, never a build. The seed rows are the compiled facts the
-- member measured before this registry existed, so adoption changes nothing.
CREATE TABLE IF NOT EXISTS config.tags (
    name       text PRIMARY KEY,
    description text NOT NULL,
    source     text NOT NULL CHECK (source IN ('declared', 'probed')),
    terms      text NOT NULL DEFAULT '',
    probe      text NOT NULL DEFAULT '' CHECK (probe IN ('', 'path', 'command', 'limit', 'builtin')),
    probe_os   text NOT NULL DEFAULT '',
    probe_path text NOT NULL DEFAULT '',
    probe_argv text NOT NULL DEFAULT '',
    probe_match text NOT NULL DEFAULT '',
    probe_min  text NOT NULL DEFAULT '',
    probe_limit text NOT NULL DEFAULT '' CHECK (probe_limit IN ('', 'nofile')),
    builtin    text NOT NULL DEFAULT '' CHECK (builtin IN ('', 'os', 'isa', 'landlock', 'kvm')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO config.tags (name, description, source, terms, probe, probe_os, probe_path, probe_argv, probe_match, probe_min, probe_limit, builtin) VALUES
    ('linux',   'the member runs Linux',                                  'probed',   '', '', 'builtin', '', '', '', '', '', 'os'),
    ('darwin',  'the member runs macOS',                                  'probed',   '', '', 'builtin', '', '', '', '', '', 'os'),
    ('windows', 'the member runs Windows',                                'probed',   '', '', 'builtin', '', '', '', '', '', 'os'),
    ('amd64',   'the member''s ISA is amd64',                             'probed',   '', '', 'builtin', '', '', '', '', '', 'isa'),
    ('arm64',   'the member''s ISA is arm64',                             'probed',   '', '', 'builtin', '', '', '', '', '', 'isa'),
    ('landlock','the Linux kernel runs landlock',                         'probed',   '', '', 'builtin', '', '', '', '', '', 'landlock'),
    ('kvm',     'the Linux kernel''s KVM answers the member''s user',     'probed',   '', '', 'builtin', '', '', '', '', '', 'kvm'),
    ('tla',     'the pinned TLA+ tools are installed',                    'probed',   '', '', 'path',    '', '/opt/tla/tla2tools.jar', '', '', '', ''),
    ('go',      'the Go toolchain is on PATH',                            'probed',   '', '', 'command', '', '', 'go version', '', '', ''),
    ('dotnet',  'the .NET SDK is on PATH',                                'probed',   '', '', 'command', '', '', 'dotnet --version', '', '', ''),
    ('java',    'the Java runtime is on PATH',                            'probed',   '', '', 'command', '', '', 'java -version', '', '', ''),
    ('avx2',    'the machine declares AVX2',                              'declared', '', '', '', '', '', '', '', '', ''),
    ('avx512f', 'the machine declares AVX-512F',                          'declared', '', '', '', '', '', '', '', '', ''),
    ('xdp',     'the machine declares XDP support',                       'declared', '', '', '', '', '', '', '', '', '')
ON CONFLICT (name) DO NOTHING;
