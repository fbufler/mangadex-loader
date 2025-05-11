# MangaDex Loader

**MangaDex Loader** is a command-line tool for downloading and bundling manga from [MangaDex](https://mangadex.org). It organizes downloaded chapters into volumes and exports them as `.cbz` (Comic Book Zip) archives.

---

## 🚀 Features

- Download all chapters of a manga in a specified language
- Automatically sort and group chapters into volumes
- Create `.cbz` files per volume (compatible with most comic readers)
- Clean, readable filenames for each volume
- CLI built with [Cobra](https://github.com/spf13/cobra)

---

## 📦 Installation

Clone the repo and build the binary:

```bash
git clone https://github.com/fbufler/mangadex-loader.git
cd mangadex-loader
go build -o mangadex ./cmd
```

---

## 📘 Usage

```bash
./mangadex get -m <manga-id> -o <output-dir> -n <base-name>
```

### 🔧 Options:

| Flag        | Description                              | Required |
|-------------|------------------------------------------|----------|
| `-m, --manga`  | MangaDex manga UUID                      | ✅       |
| `-o, --output` | Output directory where `.cbz` files go   | ✅       |
| `-n, --name`   | Base name used in `.cbz` filenames       | ✅       |

### Example

```bash
./mangadex get \
  --manga 22c844da-1122-4ab3-b726-e7d4b7114254 \
  --output ./downloads \
  --name "classroom-of-the-elite-year-2"
```

This will create files like:

```
downloads/
├── classroom-of-the-elite-year-2-volume-1.cbz
├── classroom-of-the-elite-year-2-volume-2.cbz
└── ...
```

---

## 🧪 Development

### Requirements

- Go 1.20+
- Internet access to query MangaDex

### Run locally

```bash
go run ./cmd --help
```

---

## ❓ FAQ

**Q: Does it support downloading specific chapters only?**  
_Not yet. But filtering by volume or chapter number is on the roadmap._

**Q: What language is used?**  
_Currently hardcoded to English (`en`)._

**Q: Does this violate MangaDex terms of service?**  
_Be mindful of usage limits and fair use — this tool uses their public API responsibly._

---

## 📄 License

MIT © [fbufler](https://github.com/fbufler)
