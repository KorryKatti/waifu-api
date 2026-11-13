export type NsfwClass = 'Drawing' | 'Hentai' | 'Neutral' | 'Porn' | 'Sexy';

export interface NsfwPrediction {
  className: NsfwClass;
  probability: number;
}

export interface NsfwAssessment {
  isNsfw: boolean;
  topClass: NsfwClass;
  topProbability: number;
  predictions: NsfwPrediction[];
}

type NsfwModel = {
  classify: (img: HTMLImageElement | HTMLCanvasElement | ImageData) => Promise<NsfwPrediction[]>;
};

let modelPromise: Promise<NsfwModel> | null = null;

export async function loadNsfwModel(): Promise<NsfwModel> {
  if (!modelPromise) {
    modelPromise = (async () => {
      // Lazy-load heavy deps only when needed.
      await import('@tensorflow/tfjs');
      const nsfwjs = await import('nsfwjs');
      // MobileNetV2Mid is a good accuracy/size tradeoff (~4.2MB) per upgrade plan.
      const model = await nsfwjs.load('MobileNetV2Mid', { type: 'graph' } as any);
      return model as unknown as NsfwModel;
    })().catch((err) => {
      modelPromise = null;
      throw err;
    });
  }
  return modelPromise;
}

export function assessPredictions(predictions: NsfwPrediction[]): NsfwAssessment {
  const sorted = [...predictions].sort((a, b) => b.probability - a.probability);
  const top = sorted[0] || { className: 'Neutral' as NsfwClass, probability: 0 };
  const byClass = new Map(predictions.map((p) => [p.className, p.probability]));
  const sexy = byClass.get('Sexy') ?? 0;
  const porn = byClass.get('Porn') ?? 0;
  const hentai = byClass.get('Hentai') ?? 0;

  // Per request: Sexy above 69% classified nsfw. Also treat explicit classes as nsfw.
  const isNsfw = porn >= 0.5 || hentai >= 0.5 || sexy > 0.69;

  return {
    isNsfw,
    topClass: top.className,
    topProbability: top.probability,
    predictions: sorted,
  };
}

export async function classifyImageElement(img: HTMLImageElement): Promise<NsfwAssessment> {
  const model = await loadNsfwModel();
  const predictions = await model.classify(img);
  return assessPredictions(predictions as NsfwPrediction[]);
}
