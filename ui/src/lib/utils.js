import { clsx } from "clsx";
import { twMerge } from "tailwind-merge";

/** cn merges conditional class names, with later Tailwind utilities winning. */
export function cn(...inputs) {
  return twMerge(clsx(inputs));
}
