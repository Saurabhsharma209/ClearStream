#!/usr/local/bin/python3.13

"""
finetune_deepfilter.py — Fine-tune DeepFilterNet on ClearStream telephony data.

Requires:
  pip install deepfilternet torch torchaudio onnx onnxruntime

Usage:
  # Quick test (1 epoch, 200 steps — verify setup works):
  python3 scripts/finetune_deepfilter.py --epochs 1 --steps-per-epoch 200

  # Full fine-tune on Apple M4 (~4-6 hours for 30 epochs on 10k pairs):
  python3 scripts/finetune_deepfilter.py

  # After training, test the exported model:
  python3 scripts/finetune_deepfilter.py --test-only models/deepfilter_finetuned.onnx input.wav out.wav

Copy to: ~/ClearStream/scripts/finetune_deepfilter.py
"""

import argparse
import os
import sys
import time
from pathlib import Path

import torch
import torch.nn as nn
import torchaudio
from torch.utils.data import DataLoader, Dataset

# Detect Apple Silicon MPS
if torch.backends.mps.is_available():
    DEVICE = torch.device("mps")
    print("Device: Apple MPS (M-series GPU)")
elif torch.cuda.is_available():
    DEVICE = torch.device("cuda")
    print(f"Device: CUDA ({torch.cuda.get_device_name(0)})")
else:
    DEVICE = torch.device("cpu")
    print("Device: CPU (slow — consider cloud GPU for full training)")

SAMPLE_RATE = 16000
DATA_DIR    = Path.home() / "ClearStream" / "data"
MODELS_DIR  = Path.home() / "ClearStream" / "models"


# ── Dataset ──────────────────────────────────────────────────────────────────

class NoisySpeechDataset(Dataset):
    def __init__(self, data_dir: Path, segment_sec: float = 4.0):
        self.clean_dir  = data_dir / "clean"
        self.noisy_dir  = data_dir / "noisy"
        self.files      = sorted(self.clean_dir.glob("*.wav"))
        self.seg_len    = int(SAMPLE_RATE * segment_sec)

        if not self.files:
            raise FileNotFoundError(
                f"No clean WAVs found in {self.clean_dir}. "
                f"Run prepare_training_data.py first."
            )
        print(f"  Dataset: {len(self.files)} pairs from {data_dir}")

    def __len__(self):
        return len(self.files)

    def __getitem__(self, idx: int):
        name = self.files[idx].name
        clean, _ = torchaudio.load(str(self.clean_dir / name))
        noisy, _ = torchaudio.load(str(self.noisy_dir / name))
        # Shape: [1, samples] → [samples] (mono)
        return noisy.squeeze(0), clean.squeeze(0)


# ── Loss ─────────────────────────────────────────────────────────────────────

def si_sdr_loss(estimate: torch.Tensor, target: torch.Tensor) -> torch.Tensor:
    """
    Scale-Invariant Signal-to-Distortion Ratio loss.
    Maximising SI-SDR improves perceptual quality and WER simultaneously.
    """
    eps = 1e-8
    # Zero-mean
    target   = target   - target.mean(dim=-1, keepdim=True)
    estimate = estimate - estimate.mean(dim=-1, keepdim=True)

    # SI-SDR
    dot       = (estimate * target).sum(dim=-1, keepdim=True)
    t_sq      = target.pow(2).sum(dim=-1, keepdim=True) + eps
    s_target  = (dot / t_sq) * target
    e_noise   = estimate - s_target

    si_sdr = 10 * torch.log10(
        s_target.pow(2).sum(dim=-1) / (e_noise.pow(2).sum(dim=-1) + eps) + eps
    )
    return -si_sdr.mean()


def spectral_loss(estimate: torch.Tensor, target: torch.Tensor) -> torch.Tensor:
    """
    Log-magnitude STFT loss — penalises spectral artefacts.
    Combined with SI-SDR gives better perceptual quality than SI-SDR alone.
    """
    n_fft   = 512
    hop     = 128
    window  = torch.hann_window(n_fft, device=estimate.device)

    def stft_mag(x):
        return torch.stft(x, n_fft, hop, return_complex=True).abs() + 1e-8

    est_mag = stft_mag(estimate)
    tgt_mag = stft_mag(target)
    return (torch.log(est_mag) - torch.log(tgt_mag)).pow(2).mean()


# ── DeepFilterNet wrapper ─────────────────────────────────────────────────────

def load_pretrained_df():
    """
    Load the pretrained DeepFilterNet model using the deepfilternet package.
    Downloads weights (~30MB) on first run.
    """
    try:
        from df.enhance import init_df, enhance
        from df import config
    except ImportError:
        print("ERROR: deepfilternet not installed. Run: pip install deepfilternet")
        sys.exit(1)

    print("Loading pretrained DeepFilterNet…")
    model, df_state, _ = init_df()
    model = model.to(DEVICE)
    return model, df_state


def export_to_onnx(model, df_state, out_path: Path):
    """
    Export the fine-tuned model to ONNX for use in pkg/model/deepfilter.go.

    DeepFilterNet's actual ONNX export uses the df package's built-in export.
    The exported model takes raw audio (float32, shape [1, samples]) and
    returns enhanced audio (same shape).
    """
    try:
        from df.enhance import init_df
        from df import config
    except ImportError:
        print("ERROR: deepfilternet not installed")
        return

    out_path.parent.mkdir(parents=True, exist_ok=True)
    print(f"Exporting ONNX model → {out_path}")

    model.eval()
    model = model.cpu()

    # DeepFilterNet takes [batch, time] float32 PCM
    # Use a 1-second dummy input
    dummy = torch.zeros(1, SAMPLE_RATE)

    try:
        # Use the df package's ONNX export if available
        from df.model import ModelParams
        torch.onnx.export(
            model,
            (dummy,),
            str(out_path),
            input_names=["input"],
            output_names=["output"],
            dynamic_axes={
                "input":  {0: "batch", 1: "samples"},
                "output": {0: "batch", 1: "samples"},
            },
            opset_version=14,
            do_constant_folding=True,
        )
        print(f"ONNX export complete: {out_path}")
        print(f"Model size: {out_path.stat().st_size / 1e6:.1f} MB")
    except Exception as e:
        print(f"ONNX export error: {e}")
        print("Saving PyTorch checkpoint instead (convert later).")
        torch.save(model.state_dict(), out_path.with_suffix(".pt"))


# ── Training loop ─────────────────────────────────────────────────────────────

def train(
    epochs: int = 30,
    batch_size: int = 8,
    lr: float = 5e-5,           # Low LR for fine-tuning (don't destroy pretrained features)
    steps_per_epoch: int = None,
    lambda_spec: float = 0.3,   # Weight of spectral loss vs SI-SDR
):
    model, df_state = load_pretrained_df()

    # Freeze encoder, only fine-tune decoder + recurrent layers
    # This is key for fine-tuning: preserve the learned spectral features,
    # adapt the masking network to telephony noise characteristics.
    frozen = 0
    trained = 0
    for name, param in model.named_parameters():
        if "enc" in name.lower() and "decoder" not in name.lower():
            param.requires_grad = False
            frozen += param.numel()
        else:
            param.requires_grad = True
            trained += param.numel()
    print(f"Parameters: {trained/1e6:.1f}M trainable, {frozen/1e6:.1f}M frozen")

    # Data
    train_ds = NoisySpeechDataset(DATA_DIR / "train")
    val_ds   = NoisySpeechDataset(DATA_DIR / "val")
    train_dl = DataLoader(train_ds, batch_size=batch_size, shuffle=True,
                          num_workers=4, pin_memory=True)
    val_dl   = DataLoader(val_ds,   batch_size=batch_size, shuffle=False,
                          num_workers=2)

    # Optimizer — AdamW with cosine decay
    opt   = torch.optim.AdamW(
        [p for p in model.parameters() if p.requires_grad], lr=lr, weight_decay=1e-4
    )
    sched = torch.optim.lr_scheduler.CosineAnnealingLR(opt, T_max=epochs, eta_min=lr * 0.1)

    MODELS_DIR.mkdir(parents=True, exist_ok=True)
    best_val  = float("inf")
    best_path = MODELS_DIR / "deepfilter_best.pt"

    print(f"\nStarting fine-tuning for {epochs} epochs on {DEVICE}")
    print(f"  train: {len(train_ds)} pairs | val: {len(val_ds)} pairs")
    print(f"  batch_size={batch_size} | lr={lr} | lambda_spec={lambda_spec}\n")

    for epoch in range(1, epochs + 1):
        # ── Train ──
        model.train()
        t_loss = 0.0
        t0 = time.time()
        for step, (noisy, clean) in enumerate(train_dl):
            if steps_per_epoch and step >= steps_per_epoch:
                break

            noisy = noisy.to(DEVICE)
            clean = clean.to(DEVICE)

            # DeepFilterNet enhance: model takes PCM, returns enhanced PCM
            # The df package wraps the model + STFT + masking
            try:
                from df.enhance import enhance as df_enhance
                enhanced = df_enhance(model, df_state, noisy)
            except Exception:
                # Fallback: direct forward if enhance signature differs
                enhanced = model(noisy)

            loss = si_sdr_loss(enhanced, clean) + lambda_spec * spectral_loss(enhanced, clean)

            opt.zero_grad()
            loss.backward()
            torch.nn.utils.clip_grad_norm_(
                [p for p in model.parameters() if p.requires_grad], 1.0
            )
            opt.step()
            t_loss += loss.item()

        sched.step()
        steps = min(steps_per_epoch or len(train_dl), len(train_dl))
        avg_train = t_loss / steps
        elapsed = time.time() - t0

        # ── Validate ──
        model.eval()
        v_loss = 0.0
        with torch.no_grad():
            for noisy, clean in val_dl:
                noisy = noisy.to(DEVICE)
                clean = clean.to(DEVICE)
                try:
                    from df.enhance import enhance as df_enhance
                    enhanced = df_enhance(model, df_state, noisy)
                except Exception:
                    enhanced = model(noisy)
                v_loss += si_sdr_loss(enhanced, clean).item()
        avg_val = v_loss / len(val_dl)

        print(f"Epoch {epoch:3d}/{epochs} | "
              f"train={avg_train:.3f} | val={avg_val:.3f} | "
              f"lr={sched.get_last_lr()[0]:.1e} | {elapsed:.0f}s")

        # Save best checkpoint
        if avg_val < best_val:
            best_val = avg_val
            torch.save({"epoch": epoch, "model": model.state_dict(),
                        "val_loss": avg_val}, best_path)
            print(f"  ✓ New best val loss: {avg_val:.3f} — saved to {best_path}")

        # Checkpoint every 10 epochs
        if epoch % 10 == 0:
            ckpt = MODELS_DIR / f"deepfilter_epoch{epoch}.pt"
            torch.save({"epoch": epoch, "model": model.state_dict()}, ckpt)

    # ── Export best model to ONNX ──
    print(f"\nLoading best checkpoint (val_loss={best_val:.3f}) …")
    ckpt = torch.load(best_path, map_location="cpu")
    model.load_state_dict(ckpt["model"])
    export_to_onnx(model, df_state, MODELS_DIR / "deepfilter_finetuned.onnx")

    print("\nFine-tuning complete!")
    print(f"  ONNX model: {MODELS_DIR}/deepfilter_finetuned.onnx")
    print(f"  To use in ClearStream, set in Config:")
    print(f"    SuppressorConfig{{Backend: \"deepfilter\", ModelPath: \"{MODELS_DIR}/deepfilter_finetuned.onnx\"}}")


# ── Quick test (inference only) ───────────────────────────────────────────────

def test_inference(model_path: str, input_wav: str, output_wav: str):
    """
    Test a trained ONNX model on a real audio file.
    Usage: python3 finetune_deepfilter.py --test-only model.onnx input.wav out.wav
    """
    import onnxruntime as ort
    import numpy as np

    print(f"Testing {model_path} on {input_wav}")
    wav, sr = torchaudio.load(input_wav)
    if wav.shape[0] > 1:
        wav = wav.mean(0, keepdim=True)
    if sr != SAMPLE_RATE:
        wav = torchaudio.functional.resample(wav, sr, SAMPLE_RATE)

    sess = ort.InferenceSession(model_path)
    inp_name = sess.get_inputs()[0].name
    out_name = sess.get_outputs()[0].name
    print(f"  Input:  {inp_name} {sess.get_inputs()[0].shape}")
    print(f"  Output: {out_name} {sess.get_outputs()[0].shape}")

    audio_np = wav.numpy().astype(np.float32)  # [1, samples]
    result   = sess.run([out_name], {inp_name: audio_np})[0]
    result_t = torch.from_numpy(result)

    torchaudio.save(output_wav, result_t, SAMPLE_RATE)
    print(f"  Enhanced audio saved to {output_wav}")


# ── Entry point ───────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser(description="Fine-tune DeepFilterNet for ClearStream")
    ap.add_argument("--epochs",          type=int,   default=30)
    ap.add_argument("--batch-size",      type=int,   default=8)
    ap.add_argument("--lr",              type=float, default=5e-5)
    ap.add_argument("--steps-per-epoch", type=int,   default=None,
                    help="Limit steps per epoch (useful for quick testing)")
    ap.add_argument("--lambda-spec",     type=float, default=0.3,
                    help="Weight of spectral loss vs SI-SDR")
    ap.add_argument("--test-only", nargs=3, metavar=("MODEL", "IN", "OUT"),
                    help="Test mode: enhance IN with MODEL, write to OUT")
    args = ap.parse_args()

    if args.test_only:
        test_inference(*args.test_only)
    else:
        train(
            epochs=args.epochs,
            batch_size=args.batch_size,
            lr=args.lr,
            steps_per_epoch=args.steps_per_epoch,
            lambda_spec=args.lambda_spec,
        )


if __name__ == "__main__":
    main()
