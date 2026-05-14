declare module "tween-functions" {
  export const linear: (
    currentTime: number,
    beginValue: number,
    endValue: number,
    totalDuration: number
  ) => number;
}
