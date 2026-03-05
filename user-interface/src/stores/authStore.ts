import { create } from "zustand";
import {
  login as apiLogin,
  register as apiRegister,
  getMe,
  getUsers,
  getStoredToken,
  clearToken,
  type User,
} from "../api/auth";
import { setOnUnauthorized } from "../api/client";

interface AuthStore {
  authenticated: boolean;
  user: User | null;
  users: User[];
  loading: boolean;
  error: string | null;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string, displayName: string) => Promise<void>;
  logout: () => void;
  fetchUser: () => Promise<void>;
  fetchUsers: () => Promise<void>;
}

export const useAuthStore = create<AuthStore>((set, get) => {
  // Register global 401 handler so any API call that gets a 401
  // cleanly logs out via React state — no hard page reload.
  setOnUnauthorized(() => {
    clearToken();
    set({ authenticated: false, user: null, users: [], error: null });
  });

  return ({
  authenticated: !!getStoredToken(),
  user: null,
  users: [],
  loading: false,
  error: null,

  login: async (email, password) => {
    set({ loading: true, error: null });
    try {
      await apiLogin(email, password);
      set({ authenticated: true, loading: false });
      await get().fetchUser();
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Login failed",
      });
      throw err;
    }
  },

  register: async (email, password, displayName) => {
    set({ loading: true, error: null });
    try {
      await apiRegister(email, password, displayName);
      set({ authenticated: true, loading: false });
      await get().fetchUser();
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Registration failed",
      });
      throw err;
    }
  },

  logout: () => {
    clearToken();
    set({ authenticated: false, user: null, users: [], error: null });
  },

  fetchUser: async () => {
    try {
      const user = await getMe();
      set({ user });
    } catch {
      clearToken();
      set({ authenticated: false, user: null });
    }
  },

  fetchUsers: async () => {
    try {
      const users = await getUsers();
      set({ users });
    } catch {
      // silently fail — non-admin users can't list users
    }
  },
});
});
