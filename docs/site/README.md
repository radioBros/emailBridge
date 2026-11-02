# EmailBridge docs site

Static site built with **MkDocs Material** via container (no Node runtime).

```bash
# from repo root
docker run --rm -v "$PWD:/docs" -w /docs/docs/site \
  -p 8000:8000 squidfunk/mkdocs-material:9.6.14 serve -a 0.0.0.0:8000

docker run --rm -v "$PWD:/docs" -w /docs/docs/site \
  squidfunk/mkdocs-material:9.6.14 build --strict
```

Output: `docs/site/site/`. CI: `.github/workflows/docs.yml`.
