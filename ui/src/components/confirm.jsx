import * as React from "react";
import { CircleHelp, TriangleAlert } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button";
import { IconTile } from "@/components/kind";
import { cn } from "@/lib/utils";

const ConfirmContext = React.createContext(async () => false);

/**
 * ConfirmProvider replaces window.confirm with a real, focus-trapped dialog.
 * `const confirm = useConfirm()` then `await confirm({...})` returns a boolean,
 * so call sites read exactly like the ones they replaced.
 */
export function ConfirmProvider({ children }) {
  const [state, setState] = React.useState(null);
  const resolver = React.useRef(null);

  const confirm = React.useCallback((opts) => {
    setState({
      title: "Are you sure?",
      description: "",
      confirmLabel: "Confirm",
      cancelLabel: "Cancel",
      destructive: false,
      ...opts,
    });
    return new Promise((resolve) => {
      resolver.current = resolve;
    });
  }, []);

  const settle = (value) => {
    setState(null);
    if (resolver.current) {
      resolver.current(value);
      resolver.current = null;
    }
  };

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AlertDialog
        open={state !== null}
        onOpenChange={(open) => {
          // Covers Escape and overlay clicks as well as the Cancel button.
          if (!open) settle(false);
        }}
      >
        {state && (
          <AlertDialogContent>
            <div className="flex items-start gap-4">
              <IconTile
                icon={state.destructive ? TriangleAlert : CircleHelp}
                tone={state.destructive ? "destructive" : "brand"}
              />
              <AlertDialogHeader className="min-w-0 flex-1 pt-1">
                <AlertDialogTitle>{state.title}</AlertDialogTitle>
                {state.description ? (
                  <AlertDialogDescription>{state.description}</AlertDialogDescription>
                ) : null}
              </AlertDialogHeader>
            </div>
            <AlertDialogFooter>
              <AlertDialogCancel onClick={() => settle(false)}>
                {state.cancelLabel}
              </AlertDialogCancel>
              <AlertDialogAction
                onClick={() => settle(true)}
                className={cn(
                  state.destructive && buttonVariants({ variant: "destructive" }),
                )}
              >
                {state.confirmLabel}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        )}
      </AlertDialog>
    </ConfirmContext.Provider>
  );
}

export function useConfirm() {
  return React.useContext(ConfirmContext);
}
