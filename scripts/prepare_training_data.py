#!/usr/local/bin/python3.13

"""
prepare_training_data.py — Build synthetic noisy/clean pairs for DeepFilterNet fine-tuning.

Downloads:
  - ESC-50 noise dataset (~600 MB) — 50 environmental noise classes
  - LibriSpeech test-clean (~350 MB) — clean English speech
    (use --librispeech-split train-clean-100 for ~6GB full set)

Outputs to:
  ~/ClearStream/data/train/clean/*.wav + noisy/*.wav
  ~/ClearStream/data/val/clean/*.wav   + noisy/*.wav  (10% holdout)

Usage:
  python3 scripts/prepare_training_data.py
  python3 scripts/prepare_training_data.py --n-pairs 20000 --babble-pct 0.4

Copy this file to ~/ClearStream/scripts/prepare_training_data.py
"""

import argparse
import os
import random
import ssl
import sys
import tarfile
import urllib.request
import zipfile
from pathlib import Path

# Fix SSL cert verification on macOS Python 3.13 (python.org installer missing certs).
# Try certifi first; fall back to unverified for these known public dataset URLs.
try:
    import certifi
    os.environ.setdefault("SSL_CERT_FILE", certifi.where())
    _ssl_ctx = ssl.create_default_context(cafile=certifi.where())
except ImportError:
    _ssl_ctx = None

# Build openers: one verified (certifi), one unverified fallback
_ssl_ctx_unverified = ssl._create_unverified_context()
_OPENER_UNVERIFIED = urllib.request.build_opener(
    urllib.request.HTTPSHandler(context=_ssl_ctx_unverified)
)
if _ssl_ctx is not None:
    _OPENER = urllib.request.build_opener(urllib.request.HTTPSHandler(context=_ssl_ctx))
else:
    _OPENER = _OPENER_UNVERIFIED
urllib.request.install_opener(_OPENER_UNVERIFIED)  # default to unverified for all urlopen

import torch
import torchaudio

# ── Constants ────────────────────────────────────────────────────────────────

SAMPLE_RATE = 16000
SEGMENT_SEC = 4
SNR_RANGE   = (-5, 30)      # dB — cover full range from very noisy to nearly clean

DATA_DIR    = Path.home() / "ClearStream" / "data"
ESC50_URL   = "https://github.com/karoldvl/ESC-50/archive/master.zip"
LIBRI_BASE  = "https://www.openslr.org/resources/12"


# ── Download helpers ─────────────────────────────────────────────────────────

def download(url: str, dest: Path, desc: str = "") -> Path:
    if dest.exists():
        print(f"  [skip] {dest.name} already exists")
        return dest
    dest.parent.mkdir(parents=True, exist_ok=True)
    print(f"  Downloading {desc or url} …")
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    # Try verified SSL first, fall back to unverified (macOS 3.13 cert chain issues)
    try:
        resp_ctx = _OPENER.open(req)
    except Exception:
        resp_ctx = _OPENER_UNVERIFIED.open(req)
    with resp_ctx as resp:
        total = int(resp.headers.get("Content-Length", 0))
        downloaded = 0
        with open(dest, "wb") as f:
            while True:
                chunk = resp.read(65536)
                if not chunk:
                    break
                f.write(chunk)
                downloaded += len(chunk)
                if total:
                    pct = downloaded * 100 // total
                    sys.stdout.write(f"\r    {pct}%  ({downloaded//1_000_000}MB/{total//1_000_000}MB)  ")
                    sys.stdout.flush()
    print()
    return dest


def extract_zip(zip_path: Path, out_dir: Path):
    if out_dir.exists() and any(out_dir.iterdir()):
        print(f"  [skip] {out_dir.name} already extracted"); return
    out_dir.mkdir(parents=True, exist_ok=True)
    print(f"  Extracting {zip_path.name} …")
    try:
        with zipfile.ZipFile(zip_path) as zf:
            zf.extractall(out_dir)
    except (zipfile.BadZipFile, EOFError) as e:
        print(f"  [warn] Corrupt archive: {e}. Deleting and will re-download on next run.")
        import shutil
        shutil.rmtree(out_dir, ignore_errors=True)
        zip_path.unlink(missing_ok=True)
        raise


def extract_tar(tar_path: Path, out_dir: Path):
    if out_dir.exists() and any(out_dir.iterdir()):
        print(f"  [skip] {out_dir.name} already extracted"); return
    out_dir.mkdir(parents=True, exist_ok=True)
    print(f"  Extracting {tar_path.name} …")
    try:
        with tarfile.open(tar_path) as tf:
            tf.extractall(out_dir, filter="data")
    except (EOFError, tarfile.TarError) as e:
        # Corrupt archive — delete both so next run re-downloads
        print(f"  [warn] Corrupt archive: {e}. Deleting and will re-download on next run.")
        import shutil
        shutil.rmtree(out_dir, ignore_errors=True)
        tar_path.unlink(missing_ok=True)
        raise


# ── Dataset acquisition ──────────────────────────────────────────────────────

def get_esc50(cache_dir: Path) -> list:
    zip_path = cache_dir / "esc50.zip"
    download(ESC50_URL, zip_path, "ESC-50 noise dataset (~600MB)")
    extract_zip(zip_path, cache_dir / "esc50")
    audio_dir = cache_dir / "esc50" / "ESC-50-master" / "audio"
    wavs = sorted(audio_dir.glob("*.wav"))
    print(f"  ESC-50: {len(wavs)} clips")
    return wavs


def get_librispeech(cache_dir: Path, split: str = "test-clean") -> list:
    tar_path = cache_dir / f"{split}.tar.gz"
    download(f"{LIBRI_BASE}/{split}.tar.gz", tar_path, f"LibriSpeech {split}")
    extract_dir = cache_dir / "librispeech"
    extract_tar(tar_path, extract_dir)
    flacs = sorted((extract_dir / "LibriSpeech" / split).rglob("*.flac"))
    print(f"  LibriSpeech {split}: {len(flacs)} utterances")
    return flacs


# ── Audio utilities ──────────────────────────────────────────────────────────

def load_mono_16k(path) -> torch.Tensor:
    wav, sr = torchaudio.load(str(path))
    if wav.shape[0] > 1:
        wav = wav.mean(dim=0, keepdim=True)
    if sr != SAMPLE_RATE:
        wav = torchaudio.functional.resample(wav, sr, SAMPLE_RATE)
    return wav.squeeze(0)


def random_crop(wav: torch.Tensor, length: int):
    if wav.shape[0] < length:
        return None
    start = random.randint(0, wav.shape[0] - length)
    return wav[start:start + length]


def tile_to_length(wav: torch.Tensor, length: int) -> torch.Tensor:
    reps = (length // wav.shape[0]) + 2
    return wav.repeat(reps)[:length]


def mix_at_snr(speech: torch.Tensor, noise: torch.Tensor, snr_db: float):
    """Mix speech + noise at the requested SNR. Returns (noisy, clean)."""
    eps = 1e-8
    s_rms = speech.pow(2).mean().sqrt() + eps
    n_rms = noise.pow(2).mean().sqrt() + eps
    target_n_rms = s_rms / (10 ** (snr_db / 20.0))
    noisy = speech + noise * (target_n_rms / n_rms)
    peak = max(noisy.abs().max().item(), speech.abs().max().item(), 1e-6)
    return (noisy / peak).clamp(-1, 1), (speech / peak).clamp(-1, 1)


def simulate_g711(wav: torch.Tensor) -> torch.Tensor:
    """
    Simulate G.711 µ-law codec degradation (Exotel PSTN path).
    Downsample 16k→8k, encode/decode µ-law, upsample back.
    Training on codec-degraded audio is critical for Exotel call quality.
    """
    w8 = torchaudio.functional.resample(wav.unsqueeze(0), SAMPLE_RATE, 8000)
    enc = torchaudio.functional.mu_law_encoding(w8, 255)
    dec = torchaudio.functional.mu_law_decoding(enc, 255)
    return torchaudio.functional.resample(dec, 8000, SAMPLE_RATE).squeeze(0)


def make_babble(speech_files: list, length: int, n_speakers: int = 4) -> torch.Tensor:
    """
    Synthetic babble: mix N random speech clips at varying levels.
    Key for Indian call-center environments (background agent voices).
    """
    mix = torch.zeros(length)
    for _ in range(n_speakers):
        spk = load_mono_16k(random.choice(speech_files))
        spk = tile_to_length(spk, length)
        mix += spk * random.uniform(0.2, 0.8)
    peak = mix.abs().max().item() + 1e-8
    return (mix / peak).clamp(-1, 1)


def save_wav(path: Path, wav: torch.Tensor):
    path.parent.mkdir(parents=True, exist_ok=True)
    torchaudio.save(str(path), wav.unsqueeze(0), SAMPLE_RATE)


# ── Dataset builder ──────────────────────────────────────────────────────────

def build_dataset(
    speech_files: list,
    noise_files: list,
    out_dir: Path,
    n_pairs: int,
    codec_sim_pct: float = 0.5,
    babble_pct: float = 0.35,
):
    seg_len = SAMPLE_RATE * SEGMENT_SEC
    clean_dir = out_dir / "clean"
    noisy_dir = out_dir / "noisy"
    clean_dir.mkdir(parents=True, exist_ok=True)
    noisy_dir.mkdir(parents=True, exist_ok=True)

    existing = len(list(clean_dir.glob("*.wav")))
    if existing >= n_pairs:
        print(f"  [skip] {out_dir.name}: {existing} pairs already exist")
        return

    print(f"  Building {n_pairs - existing} pairs in {out_dir.name} …")
    skipped = 0
    i = existing

    while i < n_pairs:
        try:
            speech = load_mono_16k(random.choice(speech_files))
            speech = random_crop(speech, seg_len)
            if speech is None:
                skipped += 1; continue

            if random.random() < babble_pct:
                noise = make_babble(speech_files, seg_len, n_speakers=random.randint(2, 6))
            else:
                noise = load_mono_16k(random.choice(noise_files))
                noise = tile_to_length(noise, seg_len)

        except Exception as e:
            skipped += 1
            continue

        snr = random.uniform(*SNR_RANGE)
        noisy, clean = mix_at_snr(speech, noise, snr)

        if random.random() < codec_sim_pct:
            noisy = simulate_g711(noisy)
            clean = simulate_g711(clean)

        tag = f"{i:06d}_snr{snr:+.0f}dB"
        save_wav(clean_dir / f"{tag}.wav", clean)
        save_wav(noisy_dir / f"{tag}.wav", noisy)

        i += 1
        if i % 200 == 0:
            print(f"    {i}/{n_pairs} pairs  ({skipped} skipped)")

    print(f"  {out_dir.name}: {i} pairs built, {skipped} skipped.")


# ── Entry point ───────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser(description="Build ClearStream DeepFilterNet training data")
    ap.add_argument("--speech-dir", default=None)
    ap.add_argument("--noise-dir",  default=None)
    ap.add_argument("--out-dir",    default=str(DATA_DIR))
    ap.add_argument("--n-pairs",    type=int,   default=10000,
                    help="Total pairs (train+val). 10k takes ~15min on M4.")
    ap.add_argument("--val-split",  type=float, default=0.1)
    ap.add_argument("--librispeech-split", default="test-clean",
                    help="'test-clean' (~350MB) or 'train-clean-100' (~6GB)")
    ap.add_argument("--codec-sim-pct", type=float, default=0.5,
                    help="Fraction of pairs to simulate G.711 degradation")
    ap.add_argument("--babble-pct",    type=float, default=0.35,
                    help="Fraction of noise clips to use synthetic babble")
    ap.add_argument("--seed", type=int, default=42)
    args = ap.parse_args()

    random.seed(args.seed)
    torch.manual_seed(args.seed)

    cache_dir = DATA_DIR / "cache"
    out_dir   = Path(args.out_dir)

    # Acquire speech
    if args.speech_dir:
        speech_files = (sorted(Path(args.speech_dir).rglob("*.wav")) +
                        sorted(Path(args.speech_dir).rglob("*.flac")))
        print(f"Speech: {len(speech_files)} files from {args.speech_dir}")
    else:
        print("Downloading LibriSpeech…")
        speech_files = get_librispeech(cache_dir, args.librispeech_split)

    # Acquire noise
    if args.noise_dir:
        noise_files = sorted(Path(args.noise_dir).rglob("*.wav"))
        print(f"Noise: {len(noise_files)} files from {args.noise_dir}")
    else:
        print("Downloading ESC-50…")
        noise_files = get_esc50(cache_dir)

    if not speech_files or not noise_files:
        print("ERROR: no speech or noise files found"); sys.exit(1)

    n_val   = max(500, int(args.n_pairs * args.val_split))
    n_train = args.n_pairs - n_val

    build_dataset(speech_files, noise_files, out_dir / "train", n_train,
                  args.codec_sim_pct, args.babble_pct)
    build_dataset(speech_files, noise_files, out_dir / "val",   n_val,
                  args.codec_sim_pct, args.babble_pct)

    print(f"\nDataset ready at {out_dir}")
    print(f"  train: {n_train} pairs")
    print(f"  val:   {n_val} pairs")
    print(f"\nNext step: python3 scripts/finetune_deepfilter.py")


if __name__ == "__main__":
    main()
