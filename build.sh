#!/bin/bash
cd git-hook
gox \
  -ldflags="
    -X main.version  '$(git tag | sort --reverse | head -n 1)'
    -X main.compiled '$(TZ=UTC date --rfc-3339=date)'
    -X main.author   '$(git config --get user.email)'
    -X main.email    '$(git config --get user.email)'
  " \
  -os="darwin linux windows" \
  -arch="386 amd64" \
  -output "../dist/{{.Dir}}.{{.OS}}_{{.Arch}}"
